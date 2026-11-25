package database

import (
	"context"
	"strings"
	"testing"
)

func TestSQLiteDesignerCreatesTablesAndConstraints(t *testing.T) {
	ctx := context.Background()
	client, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	parent := SQLiteTable{Name: "owners", Strict: true, Columns: []SQLiteColumn{{Name: "id", Type: "INTEGER", PrimaryKey: true}}}
	if err = client.CreateSQLiteTable(ctx, parent); err != nil {
		t.Fatal(err)
	}
	definition := SQLiteTable{Name: `order"items`, Columns: []SQLiteColumn{
		{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true},
		{Name: "owner_id", Type: "INTEGER"},
		{Name: "amount", Type: "DECIMAL(12,2)", Default: "0", NotNull: true},
		{Name: "label", Type: "VARCHAR(80)", Collation: "NOCASE", Unique: true},
	}, Constraints: []SQLiteConstraint{
		{Kind: "FOREIGN KEY", Columns: "owner_id", ReferenceTable: "owners", ReferenceColumns: "id", OnDelete: "CASCADE", OnUpdate: "CASCADE"},
		{Name: "positive_amount", Kind: "CHECK", Expression: "amount >= 0"},
	}}
	if err = client.CreateSQLiteTable(ctx, definition); err != nil {
		t.Fatal(err)
	}
	ref := (Table{Schema: "main", Name: definition.Name}).Reference(SQLite)
	if _, err = client.ExecuteResult(ctx, "INSERT INTO "+ref+" (label) VALUES ('first')"); err != nil {
		t.Fatal(err)
	}
	r, err := client.Query(ctx, "SELECT id, amount FROM "+ref)
	if err != nil || len(r.Rows) != 1 || r.Rows[0][0] != "1" || r.Rows[0][1] != "0" {
		t.Fatalf("primary-key/default semantics: %+v %v", r, err)
	}
	for _, query := range []string{
		"INSERT INTO " + ref + " (amount) VALUES (-1)",
		"INSERT INTO " + ref + " (label) VALUES ('FIRST')",
		"INSERT INTO " + ref + " (owner_id) VALUES (999)",
	} {
		if _, err = client.ExecuteResult(ctx, query); err == nil {
			t.Fatalf("constraint was not enforced: %s", query)
		}
	}
	if err = client.CreateSQLiteTable(ctx, definition); err == nil {
		t.Fatal("creating an existing table should fail")
	}
	if count, err := client.CountRows(ctx, Table{Schema: "main", Name: definition.Name}); err != nil || count != 1 {
		t.Fatalf("duplicate creation changed existing data: %d %v", count, err)
	}
	composite := SQLiteTable{Name: "strict_keys", Strict: true, WithoutRowID: true,
		Columns: []SQLiteColumn{{Name: "tenant", Type: "TEXT", PrimaryKey: true}, {Name: "id", Type: "INT", PrimaryKey: true}},
	}
	if err = client.CreateSQLiteTable(ctx, composite); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ExecuteResult(ctx, `INSERT INTO strict_keys VALUES ('team', 'not an integer')`); err == nil {
		t.Fatal("STRICT type validation not enforced")
	}
	catalog, err := client.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, schema := range catalog {
		for _, table := range schema.Tables {
			found = found || table.Name == definition.Name
		}
	}
	if !found {
		t.Fatal("new table missing from refreshed catalog")
	}
}

func TestSQLiteTableDesignerValidation(t *testing.T) {
	valid := func() SQLiteTable {
		return SQLiteTable{Name: "records", Columns: []SQLiteColumn{{Name: "id", Type: "INTEGER", PrimaryKey: true}, {Name: "value", Type: "TEXT"}}}
	}
	for _, tc := range []struct {
		name   string
		change func(*SQLiteTable)
	}{
		{"reserved table", func(d *SQLiteTable) { d.Name = "sqlite_records" }},
		{"empty name", func(d *SQLiteTable) { d.Name = "" }},
		{"no columns", func(d *SQLiteTable) { d.Columns = nil }},
		{"duplicate columns", func(d *SQLiteTable) { d.Columns[1].Name = "ID" }},
		{"type injection", func(d *SQLiteTable) { d.Columns[1].Type = "TEXT); DROP TABLE projects; --" }},
		{"default injection", func(d *SQLiteTable) { d.Columns[1].Default = "0); DROP TABLE projects; --" }},
		{"strict type", func(d *SQLiteTable) { d.Strict = true; d.Columns[1].Type = "VARCHAR(80)" }},
		{"rowid key", func(d *SQLiteTable) { d.WithoutRowID = true; d.Columns[0].PrimaryKey = false }},
		{"autoincrement without rowid", func(d *SQLiteTable) { d.WithoutRowID = true; d.Columns[0].AutoIncrement = true }},
		{"autoincrement noninteger", func(d *SQLiteTable) { d.Columns[0].Type = "INT"; d.Columns[0].AutoIncrement = true }},
		{"autoincrement composite", func(d *SQLiteTable) { d.Columns[0].AutoIncrement = true; d.Columns[1].PrimaryKey = true }},
		{"multiple primary keys", func(d *SQLiteTable) { d.Constraints = []SQLiteConstraint{{Kind: "PRIMARY KEY", Columns: "value"}} }},
		{"missing column", func(d *SQLiteTable) { d.Constraints = []SQLiteConstraint{{Kind: "UNIQUE", Columns: "missing"}} }},
		{"foreign-key mismatch", func(d *SQLiteTable) {
			d.Constraints = []SQLiteConstraint{{Kind: "FOREIGN KEY", Columns: "value", ReferenceTable: "other", ReferenceColumns: "id,tenant"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := valid()
			tc.change(&d)
			if _, err := d.SQL(); err == nil {
				t.Fatal("invalid table definition accepted")
			}
		})
	}
	d := valid()
	d.Columns[1].Type = "TEXT PRIMARY KEY"
	sql, err := d.SQL()
	if err != nil || !strings.Contains(sql, `"TEXT PRIMARY KEY"`) {
		t.Fatal("custom type names must be quoted to prevent constraint injection")
	}
}

func TestSQLiteDesignerDoesNotCommitEditorTransaction(t *testing.T) {
	ctx := context.Background()
	client, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	editor, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	if _, err = editor.ExecuteResult(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err = editor.CreateSQLiteTable(ctx, SQLiteTable{Name: "separate", Columns: []SQLiteColumn{{Name: "id", Type: "INTEGER"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = editor.ExecuteResult(ctx, "ROLLBACK"); err != nil {
		t.Fatal("designer committed the editor's transaction", err)
	}
	if _, err = client.Query(ctx, `SELECT * FROM separate`); err != nil {
		t.Fatal("designer creation was rolled back with the editor", err)
	}
}
