package database

import (
	"context"
	"strings"
	"testing"
)

func TestSQLiteMetadataAndAffectedRows(t *testing.T) {
	c := demo(t)
	ctx := context.Background()
	if _, err := c.Execute(ctx, `CREATE TABLE tasks (id INTEGER PRIMARY KEY, project_id INTEGER REFERENCES projects(id), note TEXT DEFAULT 'hello')`); err != nil {
		t.Fatal(err)
	}
	m, err := c.TableMetadata(ctx, Table{Schema: "main", Name: "tasks"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Columns) != 3 || !m.Columns[0].PrimaryKey || !m.Columns[1].ForeignKey || m.Columns[2].Type != "TEXT" || !strings.Contains(m.DDL, "CREATE TABLE tasks") {
		t.Fatalf("wrong metadata: %+v", m)
	}
	result, err := c.ExecuteResult(ctx, "UPDATE projects SET status='Ready' WHERE id=1")
	if err != nil || result.RowsAffected != 1 || result.Elapsed < 0 {
		t.Fatalf("affected rows: %+v %v", result, err)
	}
	name := "odd' table"
	if _, err = c.Execute(ctx, "CREATE TABLE "+QuoteIdentifier(name)+" (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.TableMetadata(ctx, Table{Schema: "main", Name: name}); err != nil {
		t.Fatal(err)
	}
}
