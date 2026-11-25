package database

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestSQLiteExportStreamsBeyondGridAndCellLimits(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sql := `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2505) SELECT i, printf('%06000d', i), NULL, x'00ff' FROM n ORDER BY i; -- full export`
	grid, err := c.Query(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(grid.Rows) != 1000 || !grid.Truncated || len(grid.Rows[0][1]) >= 6000 {
		t.Fatal("grid limits unexpectedly changed")
	}
	count := 0
	err = c.StreamExportQuery(ctx, sql, func(columns []string, next func() ([]string, error)) error {
		if len(columns) != 4 {
			t.Fatal("wrong columns")
		}
		for {
			row, e := next()
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			count++
			if row[0] != strconv.Itoa(count) || len(row[1]) != 6000 || row[2] != "NULL" || row[3] != "<BLOB 2 bytes>" {
				t.Fatalf("export lost complete values: %q", row)
			}
		}
	})
	if err != nil || count != 2505 {
		t.Fatalf("incomplete export: %d %v", count, err)
	}
	if len(c.sessions) != 0 {
		t.Fatal("export leaked its session")
	}
}

func TestExportQueryRejectsWritesAndPreservesEditorSession(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	editor, err := c.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	if _, err = editor.Execute(ctx, "BEGIN; CREATE TEMP TABLE editor_only(id INTEGER); INSERT INTO editor_only VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`SELECT 1; DELETE FROM projects`, `WITH x AS (SELECT 1) DELETE FROM projects RETURNING id`, `PRAGMA query_only=OFF;DELETE FROM projects`, `SELECT 1; /* nested /* */ DELETE FROM projects; */`} {
		err = c.StreamExportQuery(ctx, statement, func(_ []string, next func() ([]string, error)) error {
			for {
				_, e := next()
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
			}
		})
		if err == nil {
			t.Fatalf("replayed write: %s", statement)
		}
	}
	out, err := editor.Query(ctx, "SELECT * FROM editor_only")
	if err != nil || len(out.Rows) != 1 {
		t.Fatalf("export changed editor transaction: %v", err)
	}
	if _, err = editor.Execute(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	out, err = c.Query(ctx, "SELECT count(*) FROM projects")
	if err != nil || out.Rows[0][0] != "3" {
		t.Fatal("export changed data")
	}
}

func TestExportCancellationAndSinkFailureReleaseSessions(t *testing.T) {
	c, err := Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	err = c.StreamExportQuery(ctx, "WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000000) SELECT i FROM n", func(_ []string, next func() ([]string, error)) error {
		if _, e := next(); e != nil {
			return e
		}
		cancel()
		_, e := next()
		return e
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	sink := errors.New("disk full")
	err = c.StreamExportQuery(context.Background(), "SELECT 1", func([]string, func() ([]string, error)) error { return sink })
	if !errors.Is(err, sink) || len(c.sessions) != 0 {
		t.Fatalf("sink error/session cleanup: %v", err)
	}
	if _, err = exportRow([]any{strings.Repeat("x", ExportCellBytes+1)}); err == nil {
		t.Fatal("oversized cell silently truncated")
	}
}

func TestExportStatementDelimitersAndComments(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		engine Engine
	}{
		{"-- comment\n SELECT ';', 'it''s;fine'; -- trailing", SQLite},
		{`SELECT ';' AS [name;with;semis] /* comment ; */;`, SQLite},
		{`SELECT $$a;b$$, $tag$a;b$tag$, E'escaped\';quote'; -- trailing`, PostgreSQL},
		{"# comment\n SELECT '''multi;line''', r'raw;value';", BigQuery},
		{`SELECT 'it\'s;fine', 5--3; # trailing`, MySQL},
	} {
		if _, err := exportStatement(tc.sql, tc.engine); err != nil {
			t.Fatalf("valid single query rejected %s: %v", tc.sql, err)
		}
	}
}

func TestPostgreSQLExportUsesSeparateReadOnlySession(t *testing.T) {
	editor := cursorTestSession(t)
	ctx := context.Background()
	if _, err := editor.Execute(ctx, "BEGIN; CREATE TEMP TABLE export_editor_only(id integer)"); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := editor.StreamExportQuery(ctx, `SELECT i, repeat('x',6000), NULL::text, '{"ok":true}'::jsonb FROM generate_series(1,2505) i ORDER BY i`, func(_ []string, next func() ([]string, error)) error {
		for {
			row, e := next()
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			count++
			if row[0] != strconv.Itoa(count) || len(row[1]) != 6000 || row[2] != "NULL" || !strings.Contains(row[3], `"ok"`) {
				t.Fatal("export lost values")
			}
		}
	})
	if err != nil || count != 2505 {
		t.Fatalf("PostgreSQL export: %d %v", count, err)
	}
	if _, err = editor.Query(ctx, "SELECT * FROM export_editor_only"); err != nil {
		t.Fatal("export ended editor transaction", err)
	}
	if _, err = editor.Execute(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	err = editor.StreamExportQuery(ctx, `WITH x AS (SELECT 1) UPDATE pg_database SET datname=datname RETURNING datname`, func(_ []string, next func() ([]string, error)) error { _, e := next(); return e })
	if err == nil {
		t.Fatal("PostgreSQL export allowed a write")
	}
}
