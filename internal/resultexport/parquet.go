package resultexport

import (
	"context"
	"fmt"
	"io"

	"github.com/apache/arrow/go/v15/parquet"
	"github.com/apache/arrow/go/v15/parquet/compress"
	"github.com/apache/arrow/go/v15/parquet/file"
	"github.com/apache/arrow/go/v15/parquet/schema"
)

func writeParquet(ctx context.Context, out io.Writer, d Data, o Options) (err error) {
	// Arrow v15 panics when the initial magic-number write fails.
	defer func() {
		if failure := recover(); failure != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = fmt.Errorf("write Parquet: %v", failure)
			}
		}
	}()
	fields := make(schema.FieldList, len(d.Columns))
	for i, name := range d.Columns {
		fields[i], err = schema.NewPrimitiveNodeLogical(name, parquet.Repetitions.Required, schema.StringLogicalType{}, parquet.Types.ByteArray, -1, int32(i))
		if err != nil {
			return err
		}
	}
	root, err := schema.NewGroupNode("result", parquet.Repetitions.Required, fields, -1)
	if err != nil {
		return err
	}
	codec := map[string]compress.Compression{"None": compress.Codecs.Uncompressed, "Snappy": compress.Codecs.Snappy, "GZIP": compress.Codecs.Gzip, "Zstandard": compress.Codecs.Zstd}[o.Compression]
	w := file.NewParquetWriter(out, root, file.WithWriterProps(parquet.NewWriterProperties(parquet.WithCompression(codec))))
	defer func() {
		closeErr := w.Close()
		if err == nil {
			err = closeErr
		}
	}()
	var batch [][]string
	batchBytes := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		group := w.AppendRowGroup()
		for col := range d.Columns {
			column, e := group.NextColumn()
			if e != nil {
				return e
			}
			values := make([]parquet.ByteArray, len(batch))
			for i, row := range batch {
				values[i] = parquet.ByteArray(row[col])
			}
			_, e = column.(*file.ByteArrayColumnChunkWriter).WriteBatch(values, nil, nil)
			closeErr := column.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		if e := group.Close(); e != nil {
			return e
		}
		batch = nil
		batchBytes = 0
		return nil
	}
	if err = d.eachRow(func(row []string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		copyRow := append([]string(nil), row...)
		batch = append(batch, copyRow)
		for _, value := range row {
			batchBytes += len(value)
		}
		if len(batch) >= 1000 || batchBytes >= 8<<20 {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	if err = flush(); err != nil {
		return err
	}

	return nil
}
