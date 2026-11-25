package resultexport

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Data struct {
	Columns []string
	Rows    [][]string
	SQL     string
	Next    func() ([]string, error) `json:"-"`
}

func (d Data) eachRow(visit func([]string) error) error {
	if d.Next == nil {
		for _, row := range d.Rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		row, err := d.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(row) != len(d.Columns) {
			return errors.New("Result row does not match its columns.")
		}
		if err = visit(row); err != nil {
			return err
		}
	}
}

type Options struct {
	Format, Compression                             string
	Header, BOM, CRLF, QuerySheet, Pretty, JSONRows bool
	Separator                                       string
}

func (o Options) Extension() string {
	ext := map[string]string{"CSV": "csv", "Excel (xlsx)": "xlsx", "JSON": "json", "Parquet": "parquet"}[o.Format]
	if (o.Format == "CSV" || o.Format == "JSON") && o.Compression == "GZIP" {
		ext += ".gz"
	}
	return ext
}

func (o Options) ValidateOptions() error {
	allowed := map[string][]string{"CSV": {"None", "GZIP"}, "JSON": {"None", "GZIP"}, "Excel (xlsx)": {"ZIP", "None"}, "Parquet": {"None", "Snappy", "GZIP", "Zstandard"}}
	valid := false
	for _, codec := range allowed[o.Format] {
		valid = valid || o.Compression == codec
	}
	if !valid {
		return errors.New("Choose a supported format and compression.")
	}
	if o.Format == "CSV" {
		r, n := utf8.DecodeRuneInString(o.Separator)
		if n != len(o.Separator) || r == utf8.RuneError || r == 0 || r == '\r' || r == '\n' || r == '"' {
			return errors.New("Separator must be one character, excluding quotes and line breaks.")
		}
	}
	return nil
}

func (o Options) Validate(d Data) error {
	if err := o.ValidateOptions(); err != nil {
		return err
	}
	if len(d.Columns) == 0 {
		return errors.New("No columns to export.")
	}
	seen := map[string]bool{}
	for _, name := range d.Columns {
		if o.Format == "Parquet" || o.Format == "JSON" && !o.JSONRows {
			if name == "" || seen[name] {
				return errors.New("This format requires unique, nonempty column names. Alias the query columns or use JSON rows with column names.")
			}
		}
		seen[name] = true
	}
	for _, row := range d.Rows {
		if len(row) != len(d.Columns) {
			return errors.New("Result row does not match its columns.")
		}
	}
	return nil
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}

func Write(ctx context.Context, out io.Writer, d Data, o Options) error {
	if err := o.Validate(d); err != nil {
		return err
	}
	var target io.Writer = contextWriter{ctx, out}
	var gz *gzip.Writer
	if (o.Format == "CSV" || o.Format == "JSON") && o.Compression == "GZIP" {
		gz = gzip.NewWriter(target)
		target = gz
	}
	var err error
	switch o.Format {
	case "CSV":
		if o.BOM {
			_, err = io.WriteString(target, "\xef\xbb\xbf")
		}
		if err == nil {
			w := csv.NewWriter(target)
			w.Comma, _ = utf8.DecodeRuneInString(o.Separator)
			w.UseCRLF = o.CRLF
			if o.Header {
				err = w.Write(d.Columns)
			}
			if err == nil {
				err = d.eachRow(w.Write)
			}
			w.Flush()
			if err == nil {
				err = w.Error()
			}
		}
	case "JSON":
		if o.JSONRows {
			var names []byte
			names, err = json.Marshal(d.Columns)
			if err == nil {
				_, err = io.WriteString(target, `{"columns":`+string(names)+`,"rows":`)
			}
		}
		if err == nil {
			_, err = io.WriteString(target, "[")
		}
		count := 0
		if err == nil {
			err = d.eachRow(func(row []string) error {
				var value any = row
				if !o.JSONRows {
					object := make(map[string]string, len(d.Columns))
					for i, name := range d.Columns {
						object[name] = row[i]
					}
					value = object
				}
				var body []byte
				var e error
				if o.Pretty {
					body, e = json.MarshalIndent(value, "  ", "  ")
				} else {
					body, e = json.Marshal(value)
				}
				if e != nil {
					return e
				}
				prefix := ""
				if count > 0 {
					prefix = ","
				}
				if o.Pretty {
					prefix += "\n  "
				}
				if _, e = io.WriteString(target, prefix); e != nil {
					return e
				}
				_, e = target.Write(body)
				count++
				return e
			})
		}
		if err == nil && o.Pretty && count > 0 {
			_, err = io.WriteString(target, "\n")
		}
		ending := "]\n"
		if o.JSONRows {
			ending = "]}\n"
		}
		if err == nil {
			_, err = io.WriteString(target, ending)
		}
	case "Excel (xlsx)":
		err = writeXLSX(target, d, o)
	case "Parquet":
		err = writeParquet(ctx, target, d, o)
	}
	if gz != nil {
		closeErr := gz.Close()
		if err == nil {
			err = closeErr
		}
	}
	return err
}

// Encode before touching the destination so invalid/cancelled exports leave it intact.
func Save(ctx context.Context, path string, replace bool, d Data, o Options) (err error) {
	return SaveGenerated(ctx, path, replace, func(target io.Writer) error { return Write(ctx, target, d, o) })
}

// SaveGenerated prepares the output before starting a potentially long query,
// and publishes it only after generation succeeds. The exclusive final create
// still protects against another process creating the destination meanwhile.
func SaveGenerated(ctx context.Context, path string, replace bool, generate func(io.Writer) error) (err error) {
	if strings.TrimSpace(path) == "" {
		return errors.New("Choose an output file.")
	}
	if info, e := os.Lstat(path); e == nil {
		if info.IsDir() {
			return errors.New("Output file is a directory.")
		}
		if !replace {
			return errors.New("Output file exists. Choose another name or enable Replace existing file.")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dbcraft-export-*")
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	if err = generate(tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if replace {
		if err = tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), path)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	dest, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("Output file exists. Choose another name or enable Replace existing file.")
		}
		return err
	}
	_, err = io.Copy(contextWriter{ctx, dest}, tmp)
	if err == nil {
		err = dest.Sync()
	}
	closeErr := dest.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
