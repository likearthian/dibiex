package resultexport

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/apache/arrow/go/v15/parquet"
	"github.com/apache/arrow/go/v15/parquet/compress"
	"github.com/apache/arrow/go/v15/parquet/file"
)

func fixture() Data {
	return Data{Columns: []string{"id", "note"}, Rows: [][]string{{"001", "NULL"}, {"=1+1", "a;\"b\"\n東京"}, {"03", "[BLOB: 4 bytes]"}}, SQL: "SELECT id, note\nFROM cameras;"}
}

func TestCSVOptionsAndGzip(t *testing.T) {
	for _, header := range []bool{false, true} {
		var out bytes.Buffer
		o := Options{Format: "CSV", Compression: "GZIP", Header: header, Separator: ";", BOM: true, CRLF: true}
		if err := Write(context.Background(), &out, fixture(), o); err != nil {
			t.Fatal(err)
		}
		r, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) || !bytes.Contains(body, []byte("\r\n")) {
			t.Fatal("missing BOM or CRLF")
		}
		reader := csv.NewReader(bytes.NewReader(body[3:]))
		reader.Comma = ';'
		rows, err := reader.ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		want := fixture().Rows
		if header {
			want = append([][]string{fixture().Columns}, want...)
		}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("CSV changed values: %q", rows)
		}
	}
	for _, sep := range []string{"", "ab", "\n", "\r", "\""} {
		if (Options{Format: "CSV", Compression: "None", Separator: sep}).Validate(fixture()) == nil {
			t.Fatalf("accepted separator %q", sep)
		}
	}
}

func TestJSONPreservesDisplayValuesAndDuplicateColumns(t *testing.T) {
	var out bytes.Buffer
	if err := Write(context.Background(), &out, fixture(), Options{Format: "JSON", Compression: "GZIP", Pretty: true}); err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var rows []map[string]string
	if err = json.NewDecoder(r).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["id"] != "001" || rows[0]["note"] != "NULL" || rows[1]["note"] != fixture().Rows[1][1] {
		t.Fatal("JSON changed display values")
	}
	d := fixture()
	d.Columns = []string{"value", "value"}
	out.Reset()
	o := Options{Format: "JSON", Compression: "None"}
	if Write(context.Background(), &out, d, o) == nil {
		t.Fatal("duplicate JSON keys silently lost data")
	}
	o.JSONRows = true
	if err = Write(context.Background(), &out, d, o); err != nil {
		t.Fatal(err)
	}
	var decoded Data
	if err = json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Columns, d.Columns) || !reflect.DeepEqual(decoded.Rows, d.Rows) {
		t.Fatal("row layout lost duplicate columns")
	}
}

func TestXLSXIsWorkbookWithTextCellsAndQuerySheet(t *testing.T) {
	for _, codec := range []string{"ZIP", "None"} {
		var out bytes.Buffer
		d := fixture()
		d.Rows[0][1] = "literal _x0041_\x01<&>"
		if err := Write(context.Background(), &out, d, Options{Format: "Excel (xlsx)", Compression: codec, QuerySheet: true}); err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("DBCRAFT_EXPORT_FIXTURES"); dir != "" && codec == "ZIP" {
			if err := os.WriteFile(filepath.Join(dir, "export-fixture.xlsx"), out.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
		archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string]string{}
		for _, entry := range archive.File {
			r, e := entry.Open()
			if e != nil {
				t.Fatal(e)
			}
			body, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			decoder := xml.NewDecoder(bytes.NewReader(body))
			for {
				_, e = decoder.Token()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatalf("invalid XML in %s: %v", entry.Name, e)
				}
			}
			parts[entry.Name] = string(body)
			if codec == "None" && entry.Method != zip.Store {
				t.Fatal("XLSX ignored compression")
			}
		}
		sheet := parts["xl/worksheets/sheet1.xml"]
		if strings.Contains(sheet, "<f>") || !strings.Contains(sheet, `t="inlineStr"`) || !strings.Contains(sheet, "=1+1") || !strings.Contains(sheet, "001") || !strings.Contains(sheet, "_x005F_x0041__x0001_") {
			t.Fatal("XLSX failed to preserve literal text")
		}
		if !strings.Contains(parts["xl/worksheets/sheet2.xml"], "FROM cameras;") || !strings.Contains(parts["xl/workbook.xml"], `name="Query"`) {
			t.Fatal("missing query sheet")
		}
		styles := parts["xl/styles.xml"]
		if !strings.Contains(styles, "<b/>") || !strings.Contains(styles, `rgb="FFE7E6E6"`) || !strings.Contains(styles, `horizontal="center"`) ||
			!strings.Contains(parts["xl/_rels/workbook.xml.rels"], `Target="styles.xml"`) || !strings.Contains(parts["[Content_Types].xml"], `PartName="/xl/styles.xml"`) {
			t.Fatal("missing workbook header formatting")
		}
	}
}

func TestParquetRoundTripEveryCodec(t *testing.T) {
	for name, codec := range map[string]compress.Compression{"None": compress.Codecs.Uncompressed, "Snappy": compress.Codecs.Snappy, "GZIP": compress.Codecs.Gzip, "Zstandard": compress.Codecs.Zstd} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			d := fixture()
			if err := Write(context.Background(), &out, d, Options{Format: "Parquet", Compression: name}); err != nil {
				t.Fatal(err)
			}
			r, err := file.NewParquetReader(bytes.NewReader(out.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.NumRows() != int64(len(d.Rows)) {
				t.Fatal("wrong Parquet row count")
			}
			for col := range d.Columns {
				meta, e := r.MetaData().RowGroup(0).ColumnChunk(col)
				if e != nil {
					t.Fatal(e)
				}
				if meta.Compression() != codec {
					t.Fatal("wrong compression codec")
				}
				column, e := r.RowGroup(0).Column(col)
				if e != nil {
					t.Fatal(e)
				}
				values := make([]parquet.ByteArray, len(d.Rows))
				_, n, e := column.(*file.ByteArrayColumnChunkReader).ReadBatch(int64(len(values)), values, nil, nil)
				if e != nil {
					t.Fatal(e)
				}
				if n != len(d.Rows) {
					t.Fatal("missing values")
				}
				for i, value := range values {
					if string(value) != d.Rows[i][col] {
						t.Fatalf("changed Parquet value %q", value)
					}
				}
			}
		})
	}
	var out bytes.Buffer
	d := fixture()
	d.Rows = nil
	if err := Write(context.Background(), &out, d, Options{Format: "Parquet", Compression: "Snappy"}); err != nil {
		t.Fatal(err)
	}
	r, err := file.NewParquetReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.NumRows() != 0 {
		t.Fatal("empty result got rows")
	}
}

func TestSaveProtectsExistingFileAndCleansTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.csv")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Format: "CSV", Compression: "None", Separator: ",", Header: true}
	if Save(context.Background(), path, false, fixture(), o) == nil {
		t.Fatal("replaced file without explicit option")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(Save(ctx, path, true, fixture(), o), context.Canceled) {
		t.Fatal("cancelled export succeeded")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "original" {
		t.Fatal("failed export damaged existing file")
	}
	if err := Save(context.Background(), path, true, fixture(), o); err != nil {
		t.Fatal(err)
	}
	if err := Save(context.Background(), filepath.Join(dir, "new.csv"), false, fixture(), o); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".dbcraft-export-*"))
	if len(files) != 0 {
		t.Fatal("temporary files leaked")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportWriteFailuresReturnErrors(t *testing.T) {
	for _, o := range []Options{{Format: "CSV", Compression: "None", Separator: ","}, {Format: "JSON", Compression: "None"}, {Format: "Excel (xlsx)", Compression: "ZIP"}, {Format: "Parquet", Compression: "Snappy"}} {
		if err := Write(context.Background(), brokenWriter{}, fixture(), o); err == nil {
			t.Fatalf("%s ignored output failure", o.Format)
		}
	}
}
