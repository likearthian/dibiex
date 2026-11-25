package resultexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/arrow/go/v15/parquet/file"
)

func streamFixture(total int) Data {
	n := 0
	return Data{Columns: []string{"id", "note"}, SQL: "SELECT id, note FROM items", Next: func() ([]string, error) {
		if n == total {
			return nil, io.EOF
		}
		n++
		return []string{strconv.Itoa(n), "full value"}, nil
	}}
}

func TestEveryFormatStreamsBeyondLoadedRowLimit(t *testing.T) {
	for _, format := range []string{"CSV", "JSON", "Excel (xlsx)", "Parquet"} {
		t.Run(format, func(t *testing.T) {
			o := Options{Format: format, Compression: "None", Separator: ",", Header: true}
			var out bytes.Buffer
			if err := Write(context.Background(), &out, streamFixture(2505), o); err != nil {
				t.Fatal(err)
			}
			switch format {
			case "CSV":
				rows, e := csv.NewReader(bytes.NewReader(out.Bytes())).ReadAll()
				if e != nil || len(rows) != 2506 || rows[2505][0] != "2505" {
					t.Fatalf("incomplete CSV: %d %v", len(rows), e)
				}
			case "JSON":
				var rows []map[string]string
				e := json.Unmarshal(out.Bytes(), &rows)
				if e != nil || len(rows) != 2505 || rows[2504]["id"] != "2505" {
					t.Fatalf("incomplete JSON: %d %v", len(rows), e)
				}
			case "Excel (xlsx)":
				archive, e := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
				if e != nil {
					t.Fatal(e)
				}
				for _, part := range archive.File {
					if part.Name == "xl/worksheets/sheet1.xml" {
						r, e := part.Open()
						if e != nil {
							t.Fatal(e)
						}
						body, e := io.ReadAll(r)
						r.Close()
						if e != nil {
							t.Fatal(e)
						}
						if bytes.Count(body, []byte("<row ")) != 2506 || !bytes.Contains(body, []byte(">2505</t>")) {
							t.Fatal("incomplete XLSX")
						}
					}
				}
			case "Parquet":
				r, e := file.NewParquetReader(bytes.NewReader(out.Bytes()))
				if e != nil {
					t.Fatal(e)
				}
				defer r.Close()
				if r.NumRows() != 2505 || r.NumRowGroups() != 3 {
					t.Fatalf("incomplete Parquet: %d", r.NumRows())
				}
			}
		})
	}
	var out bytes.Buffer
	if err := Write(context.Background(), &out, streamFixture(2505), Options{Format: "JSON", Compression: "None", JSONRows: true}); err != nil {
		t.Fatal(err)
	}
	var decoded Data
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || len(decoded.Rows) != 2505 {
		t.Fatalf("incomplete JSON rows: %v", err)
	}
}

func TestStreamingXLSXRollsOverSheetsWithHeaders(t *testing.T) {
	var out bytes.Buffer
	if err := writeXLSXWithRowLimit(&out, streamFixture(2505), Options{Compression: "ZIP", QuerySheet: true}, 1001); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, part := range archive.File {
		if !strings.HasPrefix(part.Name, "xl/worksheets/") {
			continue
		}
		r, e := part.Open()
		if e != nil {
			t.Fatal(e)
		}
		decoder := xml.NewDecoder(r)
		for {
			token, e := decoder.Token()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "row" {
				counts[part.Name]++
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "c" {
				styled := false
				for _, attr := range start.Attr {
					if attr.Name.Local == "s" && attr.Value == "1" {
						styled = true
					}
				}
				want := counts[part.Name] == 1 && part.Name != "xl/worksheets/sheet4.xml"
				if styled != want {
					t.Fatalf("incorrect header style in %s row %d", part.Name, counts[part.Name])
				}
			}
		}
		r.Close()
	}
	if counts["xl/worksheets/sheet1.xml"] != 1001 || counts["xl/worksheets/sheet2.xml"] != 1001 || counts["xl/worksheets/sheet3.xml"] != 506 || counts["xl/worksheets/sheet4.xml"] != 1 {
		t.Fatalf("incorrect sheet rollover: %v", counts)
	}
}

func TestStreamingFailuresDoNotPublishPartialFiles(t *testing.T) {
	failure := errors.New("remote connection interrupted")
	for _, format := range []string{"CSV", "JSON", "Excel (xlsx)", "Parquet"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "result")
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		d := streamFixture(3000)
		next := d.Next
		n := 0
		d.Next = func() ([]string, error) {
			n++
			if n == 1501 {
				return nil, failure
			}
			return next()
		}
		err := Save(context.Background(), path, true, d, Options{Format: format, Compression: "None", Separator: ",", Header: true})
		if !errors.Is(err, failure) {
			t.Fatalf("%s lost remote error: %v", format, err)
		}
		body, _ := os.ReadFile(path)
		if string(body) != "original" {
			t.Fatalf("%s replaced file with partial export", format)
		}
		files, _ := filepath.Glob(filepath.Join(dir, ".dbcraft-export-*"))
		if len(files) != 0 {
			t.Fatal("partial export leaked temporary file")
		}
	}
}

func TestSaveGeneratedChecksDestinationBeforeStartingQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.csv")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	generate := func(w io.Writer) error { called = true; _, err := io.WriteString(w, "new output"); return err }
	for _, invalid := range []string{path, dir, filepath.Join(dir, "missing-directory", "new.csv")} {
		if err := SaveGenerated(context.Background(), invalid, false, generate); err == nil {
			t.Fatalf("accepted invalid output: %s", invalid)
		}
		if called {
			t.Fatal("long-running query started before destination validation")
		}
	}
	if err := SaveGenerated(context.Background(), path, true, generate); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !called || string(body) != "new output" {
		t.Fatal("prepared export failed to publish output")
	}
}
