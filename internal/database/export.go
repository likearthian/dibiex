package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/jackc/pgx/v5"
	"google.golang.org/api/iterator"
)

const ExportCellBytes = 16 << 20
const ExportRowBytes = 32 << 20

// StreamExportQuery owns a fresh read-only session, independent of editor
// transactions and cursors. consume must drain next synchronously on its worker.
// Rows have bounded individual sizes, with no grid row-count or total-byte cap.
func (c *Client) StreamExportQuery(ctx context.Context, statement string, consume func(columns []string, next func() ([]string, error)) error) error {
	statement, err := exportStatement(statement, c.Engine())
	if err != nil {
		return err
	}
	session, err := c.NewQuerySession(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	if session.bq != nil {
		return session.streamBigQueryExport(ctx, statement, consume)
	}
	if session.Engine() == SQLite {
		if _, err = session.session.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return err
		}
	}
	tx, err := session.session.BeginTx(ctx, &sql.TxOptions{ReadOnly: session.Engine() == PostgreSQL})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rows *sql.Rows
	if session.Engine() == PostgreSQL {
		rows, err = tx.QueryContext(ctx, statement, pgx.QueryExecModeExec)
	} else {
		rows, err = tx.QueryContext(ctx, statement)
	}
	if err != nil {
		return fmt.Errorf("start export query: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	return consume(columns, func() ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		for i, v := range values {
			values[i] = driverCellValue(session.Engine(), v, types[i].DatabaseTypeName())
		}
		return exportRow(values)
	})
}

func (c *Client) streamBigQueryExport(ctx context.Context, statement string, consume func([]string, func() ([]string, error)) error) error {
	// Validate without executing, then run the original query so its result
	// names and ORDER BY are retained. A WITH query can otherwise contain DML.
	check := c.bigQueryQuery(statement)
	check.DryRun = true
	checked, err := check.Run(ctx)
	if err != nil {
		return fmt.Errorf("validate export query: %w", err)
	}
	status := checked.LastStatus()
	if status == nil || status.Statistics == nil {
		return errors.New("BigQuery did not provide export query statistics")
	}
	if err = status.Err(); err != nil {
		return err
	}
	stats, ok := status.Statistics.Details.(*bigquery.QueryStatistics)
	if !ok || stats.StatementType != "SELECT" {
		return errors.New("file export requires a read-only BigQuery SELECT")
	}
	job, err := c.startBigQuery(ctx, statement)
	if err != nil {
		return fmt.Errorf("start export job: %w", err)
	}
	defer func() {
		if err != nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = job.Cancel(cancelCtx)
		}
	}()
	rows, err := job.Read(ctx)
	if err != nil {
		return err
	}
	rows.PageInfo().MaxSize = 1000
	columns := make([]string, len(rows.Schema))
	for i, f := range rows.Schema {
		columns[i] = f.Name
	}
	err = consume(columns, func() ([]string, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		var values []bigquery.Value
		if e := rows.Next(&values); e != nil {
			if e == iterator.Done {
				return nil, io.EOF
			}
			return nil, e
		}
		cells := make([]any, len(values))
		for i, v := range values {
			cells[i] = v
		}
		return exportRow(cells)
	})
	return err
}

func exportRow(values []any) ([]string, error) {
	row := make([]string, len(values))
	size := 0
	for i, v := range values {
		switch x := v.(type) {
		case string:
			if len(x) > ExportCellBytes {
				return nil, errors.New("export cell exceeds 16 MiB")
			}
		case []byte:
			if len(x) > ExportCellBytes {
				return nil, errors.New("export cell exceeds 16 MiB")
			}
		}
		row[i] = formatCellText(v)
		if len(row[i]) > ExportCellBytes {
			return nil, errors.New("export cell exceeds 16 MiB")
		}
		size += len(row[i])
		if size > ExportRowBytes {
			return nil, errors.New("export row exceeds 32 MiB")
		}
	}
	return row, nil
}

// lineCommentAt reports a line-comment start at i. MySQL only treats "--" as
// a comment before whitespace; elsewhere it is subtraction.
func lineCommentAt(statement string, i int, engine Engine) bool {
	if strings.HasPrefix(statement[i:], "--") {
		if engine != MySQL || i+2 >= len(statement) {
			return true
		}
		switch statement[i+2] {
		case ' ', '\t', '\r', '\n':
			return true
		}
		return false
	}
	return (engine == BigQuery || engine == MySQL) && statement[i] == '#'
}

// Only a single query is replayed. Delimiters within quoted values/identifiers
// and comments do not split it. Trailing comments after its terminator are OK.
func exportStatement(statement string, engine Engine) (string, error) {
	end := -1
	first := ""
	for i := 0; i < len(statement); {
		ch := statement[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if lineCommentAt(statement, i, engine) {
			j := strings.IndexByte(statement[i:], '\n')
			if j < 0 {
				break
			}
			i += j + 1
			continue
		}
		if strings.HasPrefix(statement[i:], "/*") {
			j := strings.Index(statement[i+2:], "*/")
			if j < 0 {
				return "", errors.New("unterminated SQL comment")
			}
			if strings.Contains(statement[i+2:i+2+j], "/*") {
				return "", errors.New("nested comments are not supported for full-query export")
			}
			i += j + 4
			continue
		}
		if end >= 0 {
			return "", errors.New("all-row export requires one read-only query; multiple statements cannot be replayed")
		}
		if first == "" {
			j := i
			for j < len(statement) && (statement[j] >= 'A' && statement[j] <= 'Z' || statement[j] >= 'a' && statement[j] <= 'z') {
				j++
			}
			first = strings.ToUpper(statement[i:j])
			if first != "SELECT" && first != "WITH" && first != "VALUES" && !(engine == PostgreSQL && first == "TABLE") {
				return "", errors.New("file export requires a read-only SELECT, WITH or VALUES query")
			}
		}
		if ch == ';' {
			end = i
			i++
			continue
		}
		if ch == '$' && engine == PostgreSQL {
			j := i + 1
			for j < len(statement) && (statement[j] == '_' || statement[j] >= 'a' && statement[j] <= 'z' || statement[j] >= 'A' && statement[j] <= 'Z' || j > i+1 && statement[j] >= '0' && statement[j] <= '9') {
				j++
			}
			if j < len(statement) && statement[j] == '$' {
				tag := statement[i : j+1]
				k := strings.Index(statement[j+1:], tag)
				if k < 0 {
					return "", errors.New("unterminated SQL dollar string")
				}
				i = j + 1 + k + len(tag)
				continue
			}
		}
		if ch == '\'' || ch == '"' || ch == '`' || engine == SQLite && ch == '[' {
			close := string(ch)
			if ch == '[' {
				close = "]"
			}
			if engine == BigQuery && strings.HasPrefix(statement[i:], strings.Repeat(string(ch), 3)) {
				close = strings.Repeat(string(ch), 3)
			}
			escape := engine == BigQuery || engine == MySQL || engine == PostgreSQL && ch == '\'' && i > 0 && (statement[i-1] == 'e' || statement[i-1] == 'E')
			i += len(close)
			closed := false
			for i < len(statement) {
				if escape && statement[i] == '\\' {
					i += 2
					continue
				}
				if strings.HasPrefix(statement[i:], close) {
					i += len(close)
					if len(close) == 1 && i < len(statement) && statement[i] == ch && ch != '[' {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return "", errors.New("unterminated SQL quoted value")
			}
			continue
		}
		i++
	}
	if first == "" {
		return "", errors.New("result has no exportable query")
	}
	if end >= 0 {
		statement = statement[:end]
	}
	return strings.TrimSpace(statement), nil
}
