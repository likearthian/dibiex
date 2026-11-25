package database

import (
	"context"
	"testing"
)

func TestCountRowsQuotedTable(t *testing.T) {
	ctx := context.Background()
	client, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	table := Table{Schema: "main", Name: `odd"table`}
	if _, err = client.ExecuteResult(ctx, "CREATE TABLE "+table.Reference(SQLite)+" (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	session, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if count, err := session.CountRows(ctx, table); err != nil || count != 0 {
		t.Fatalf("empty table count: %d %v", count, err)
	}
	if _, err = client.ExecuteResult(ctx, "INSERT INTO "+table.Reference(SQLite)+" VALUES (NULL), ('NULL'), ('value')"); err != nil {
		t.Fatal(err)
	}
	if count, err := session.CountRows(ctx, table); err != nil || count != 3 {
		t.Fatalf("count must include NULL rows: %d %v", count, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = session.CountRows(canceled, table); err == nil {
		t.Fatal("count ignored cancellation")
	}
}
