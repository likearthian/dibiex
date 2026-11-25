package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	bqapi "google.golang.org/api/bigquery/v2"

	_ "modernc.org/sqlite"
)

const MaxRows = 1000
const maxCellBytes = 4096
const maxResultBytes = 8 << 20

type Result struct {
	Columns      []string
	ColumnTypes  []string
	Rows         [][]string
	Elapsed      time.Duration
	Truncated    bool
	More         bool
	RowsAffected int64
}

type Client struct {
	cursorMu   sync.Mutex
	cursor     *PostgreSQLCursor
	session    *sql.Conn
	parent     *Client
	sessionsMu sync.Mutex
	sessions   map[*Client]struct{}
	slots      chan struct{}
	closing    bool
	closeOnce  sync.Once
	closeErr   error
	db         *sql.DB
	bq         *bigquery.Client
	bqAPI      *bqapi.Service
	config     Config
}

// Open connects to an existing file. An empty path opens an isolated demo.
func Open(ctx context.Context, path string) (*Client, error) {
	dsn := "file:dbcraft-" + rand.Text() + "?mode=memory&cache=shared&_pragma=busy_timeout(3000)&_pragma=foreign_keys(1)"
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("database path: %w", err)
		}
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
		if !strings.HasPrefix(u.Path, "/") {
			u.Path = "/" + u.Path
		}
		u.RawQuery = url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(3000)", "foreign_keys(1)"}}.Encode()
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(MaxQuerySessions + 1)
	db.SetMaxIdleConns(1)
	c := &Client{db: db, config: Config{Engine: SQLite, Path: path}}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect SQLite: %w", err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout = 3000; PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure SQLite: %w", err)
	}
	if path == "" {
		_, err = db.ExecContext(ctx, `CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT NOT NULL, engine TEXT, status TEXT);
		INSERT INTO projects VALUES (1, 'DBCraft', 'MyGo', 'Building'), (2, 'GoRex', 'MyGo', 'Active'), (3, 'Sandbox', 'SQLite', 'Ready');`)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("create demo: %w", err)
		}
	}
	c.session, err = db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.closeClient() })
	return c.closeErr
}

// Query runs SQL and reads the returned rows. This also supports writes
// with RETURNING; like a SQL console, it executes exactly the supplied SQL.
func (c *Client) Query(ctx context.Context, statement string) (out Result, err error) {
	if err := c.closePostgreSQLCursor(); err != nil {
		return out, err
	}
	return c.query(ctx, statement, MaxRows, maxCellBytes, maxResultBytes)
}

const CatalogLimit = 10000

func (c *Client) catalogQuery(ctx context.Context, statement string) (Result, error) {
	out, err := c.query(ctx, statement, CatalogLimit, 1<<20, 32<<20)
	if err == nil && out.Truncated {
		err = fmt.Errorf("schema metadata exceeds the bounded catalog limit")
	}
	return out, err
}

func (c *Client) query(ctx context.Context, statement string, maxRows, cellBytes, resultBytes int) (out Result, err error) {
	if strings.TrimSpace(statement) == "" {
		return out, fmt.Errorf("enter a SQL query")
	}
	if c.bq != nil {
		return c.queryBigQueryBounded(ctx, statement, maxRows, cellBytes, resultBytes)
	}
	started := time.Now()
	defer func() { out.Elapsed = time.Since(started) }()
	var rows *sql.Rows
	if c.session != nil {
		rows, err = c.session.QueryContext(ctx, statement)
	} else {
		rows, err = c.db.QueryContext(ctx, statement)
	}
	if err != nil {
		return out, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	out.Columns, err = rows.Columns()
	if err != nil {
		return out, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return out, err
	}
	out.ColumnTypes = make([]string, len(types))
	for i, typ := range types {
		out.ColumnTypes[i] = typ.DatabaseTypeName()
		if c.Engine() == PostgreSQL && strings.HasPrefix(out.ColumnTypes[i], "_") {
			out.ColumnTypes[i] = strings.TrimPrefix(out.ColumnTypes[i], "_") + "[]"
		}
	}
	// SQLite expressions can lack a declared type. Infer from actual driver
	// values and leave mixed columns unclassified.
	inferred := make([]string, len(types))
	totalBytes := 0
	for rows.Next() {
		if len(out.Rows) == maxRows {
			out.Truncated = true
			break
		}
		values := make([]any, len(out.Columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return out, err
		}
		row := make([]string, len(values))
		for i, value := range values {
			if out.ColumnTypes[i] == "" && value != nil {
				kind := "UNKNOWN"
				switch value.(type) {
				case int64, float64:
					kind = "NUMBER"
				case string:
					kind = "TEXT"
				case []byte:
					kind = "BLOB"
				case bool:
					kind = "BOOLEAN"
				case time.Time:
					kind = "TIMESTAMP"
				}
				if inferred[i] == "" {
					inferred[i] = kind
				} else if inferred[i] != kind {
					inferred[i] = "UNKNOWN"
				}
			}
			value = driverCellValue(c.Engine(), value, types[i].DatabaseTypeName())
			if cellBytes == maxCellBytes {
				row[i] = formatCell(value)
			} else {
				if value == nil {
					row[i] = "NULL"
				} else {
					row[i] = fmt.Sprint(value)
				}
				if len(row[i]) > cellBytes {
					return out, fmt.Errorf("schema metadata cell exceeds the 1 MiB limit")
				}
			}
			totalBytes += len(row[i])
		}
		if totalBytes > resultBytes {
			out.Truncated = true
			break
		}
		out.Rows = append(out.Rows, row)
	}
	if err = rows.Err(); err != nil {
		return out, fmt.Errorf("read rows: %w", err)
	}
	for i, kind := range inferred {
		if out.ColumnTypes[i] == "" && kind != "UNKNOWN" {
			out.ColumnTypes[i] = kind
		}
	}
	return out, nil
}

// Execute runs SQL without reading rows using the connected engine's semantics.
func (c *Client) Execute(ctx context.Context, statement string) (time.Duration, error) {
	out, err := c.ExecuteResult(ctx, statement)
	return out.Elapsed, err
}

func (c *Client) ExecuteResult(ctx context.Context, statement string) (out Result, err error) {
	if err := c.closePostgreSQLCursor(); err != nil {
		return out, err
	}
	start := time.Now()
	defer func() { out.Elapsed = time.Since(start) }()
	if strings.TrimSpace(statement) == "" {
		return out, fmt.Errorf("enter SQL to execute")
	}
	if c.bq != nil {
		job, err := c.startBigQuery(ctx, statement)
		if err != nil {
			return out, err
		}
		defer cancelBigQueryOnError(ctx, job)
		status, err := job.Wait(ctx)
		if err == nil {
			err = status.Err()
			if status.Statistics != nil {
				if stats, ok := status.Statistics.Details.(*bigquery.QueryStatistics); ok {
					out.RowsAffected = stats.NumDMLAffectedRows
				}
			}
		}
		return out, err
	}
	var result sql.Result
	if c.session != nil {
		result, err = c.session.ExecContext(ctx, statement)
	} else {
		result, err = c.db.ExecContext(ctx, statement)
	}
	if err != nil {
		err = fmt.Errorf("execute: %w", err)
	} else {
		out.RowsAffected, _ = result.RowsAffected()
	}
	return out, err
}

func (c *Client) Tables(ctx context.Context) ([]string, error) {
	result, err := c.Query(ctx, "SELECT name FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		names = append(names, row[0])
	}
	return names, nil
}

func QuoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
