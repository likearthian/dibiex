package database

import (
	"context"
	"path/filepath"
	"testing"

	"database/sql"
)

func demo(t *testing.T) *Client {
	t.Helper()
	c, err := Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDemoQueryAndWrites(t *testing.T) {
	c := demo(t)
	ctx := context.Background()
	result, err := c.Query(ctx, "SELECT name, NULL, x'1234' FROM projects ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 || result.Rows[0][0] != "DBCraft" || result.Rows[0][1] != "NULL" || result.Rows[0][2] != "<BLOB 2 bytes>" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err = c.Execute(ctx, "CREATE TABLE notes (body TEXT); INSERT INTO notes VALUES ('hello');"); err != nil {
		t.Fatal(err)
	}
	result, err = c.Query(ctx, "SELECT body FROM notes")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "hello" {
		t.Fatalf("write/read: %+v, %v", result, err)
	}
	tables, err := c.Tables(ctx)
	if err != nil || len(tables) != 2 {
		t.Fatalf("tables: %v, %v", tables, err)
	}
}

func TestResultLimitsAndConnectionReuse(t *testing.T) {
	c := demo(t)
	result, err := c.Query(context.Background(), "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1002) SELECT x FROM n")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != MaxRows || !result.Truncated {
		t.Fatalf("expected capped result: %d, %v", len(result.Rows), result.Truncated)
	}
	result, err = c.Query(context.Background(), "SELECT length('still connected')")
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("connection not reusable: %v", err)
	}
}

func TestErrorsAndCancellation(t *testing.T) {
	c := demo(t)
	if _, err := c.Query(context.Background(), "SELECT FROM broken"); err == nil {
		t.Fatal("expected SQL error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Query(ctx, "SELECT 1"); err == nil {
		t.Fatal("expected cancellation")
	}
	if _, err := c.Execute(context.Background(), "  "); err == nil {
		t.Fatal("expected empty SQL error")
	}
}

func TestOpenFileWithSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db space #1.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE test (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	c, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	names, err := c.Tables(context.Background())
	if err != nil || len(names) != 1 || names[0] != "test" {
		t.Fatalf("file schema: %v, %v", names, err)
	}
	if _, err = Open(context.Background(), filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("opening an existing database must not create a missing file")
	}
}

func TestQuotedIdentifier(t *testing.T) {
	c := demo(t)
	name := `a"b`
	if _, err := c.Execute(context.Background(), "CREATE TABLE "+QuoteIdentifier(name)+" (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(context.Background(), "SELECT * FROM "+QuoteIdentifier(name)); err != nil {
		t.Fatal(err)
	}
}
