package database

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLColumnEditSQL(t *testing.T) {
	table := Table{Schema: `odd"schema`, Name: "items"}
	e := PostgreSQLColumnEdit{Original: Column{Name: "note", Type: "text", Nullable: "YES", Default: "'7'::text"}, Name: `new"name`, Type: "integer", Default: "7", Using: `"note"::integer`, NotNull: true, Comment: "Owner's value; -- comment"}
	statements, err := e.Statements(table)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 6 {
		t.Fatalf("unexpected edit: %v", statements)
	}
	for i, want := range []string{"DROP DEFAULT", `TYPE integer USING ("note"::integer)`, "SET DEFAULT (7)", "SET NOT NULL", `RENAME COLUMN "note" TO "new""name"`, `COMMENT ON COLUMN "odd""schema"."items"."new""name" IS E'Owner''s value; -- comment'`} {
		if !strings.Contains(statements[i], want) {
			t.Fatalf("statement %d: %q; wanted %q", i, statements[i], want)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*PostgreSQLColumnEdit)
	}{
		{"missing name", func(e *PostgreSQLColumnEdit) { e.Name = "" }},
		{"invalid type", func(e *PostgreSQLColumnEdit) { e.Type = "integer; DROP TABLE x" }},
		{"invalid expression", func(e *PostgreSQLColumnEdit) { e.Default = "1; SELECT 2" }},
		{"invalid using", func(e *PostgreSQLColumnEdit) { e.Using = "note -- comment" }},
		{"identity change", func(e *PostgreSQLColumnEdit) { e.Original.Identity = "a" }},
		{"generated change", func(e *PostgreSQLColumnEdit) { e.Original.Generated = "s" }},
		{"nullable primary key", func(e *PostgreSQLColumnEdit) { e.Original.PrimaryKey = true; e.NotNull = false }},
		{"using without type change", func(e *PostgreSQLColumnEdit) { e.Type = e.Original.Type }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := e
			tc.edit(&invalid)
			if _, err := invalid.Statements(table); err == nil {
				t.Fatal("invalid edit accepted")
			}
		})
	}
	protected := PostgreSQLColumnEdit{Original: Column{Name: "id", Type: "integer", Nullable: "NO", Identity: "a", PrimaryKey: true}, Name: "identifier", Type: "integer", NotNull: true, Comment: "ID"}
	if statements, err := protected.Statements(table); err != nil || len(statements) != 2 {
		t.Fatalf("safe identity edit rejected: %v %v", statements, err)
	}
	unchanged := PostgreSQLColumnEdit{Original: Column{Name: "x", Type: "text", Nullable: "YES"}, Name: "x", Type: "text"}
	if preview, err := unchanged.SQL(table); err != nil || preview != "-- No changes" {
		t.Fatalf("unchanged column produced DDL: %s %v", preview, err)
	}
}

func TestPostgreSQLTableEditSchemaSQL(t *testing.T) {
	edit := TableAction{Kind: "Properties", Name: "new name", ReferenceSchema: `next"schema`, Expression: "Comment", Function: "owner", RowSecurity: true}
	sql, err := edit.SQL(Table{Schema: "public", Name: "old"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`ALTER TABLE "public"."old" RENAME TO "new name"`, `ALTER TABLE "public"."new name" SET SCHEMA "next""schema"`, `COMMENT ON TABLE "next""schema"."new name"`, `ALTER TABLE "next""schema"."new name" OWNER TO "owner"`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q: %s", want, sql)
		}
	}
}

func TestPostgreSQLTableColumnEditIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_TEST_POSTGRES_DSN to test native table/column edits")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test connection")
	}
	db.SetMaxOpenConns(17)
	client := &Client{db: db, config: Config{Engine: PostgreSQL}}
	defer client.Close()
	schema := "dbcraft_edit_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	other := schema + "_target"
	table := Table{Schema: schema, Name: "items"}
	if _, err = client.ExecuteResult(ctx, "CREATE SCHEMA "+QuoteIdentifier(schema)+"; CREATE SCHEMA "+QuoteIdentifier(other)+"; CREATE TABLE "+table.Reference(PostgreSQL)+" (id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,note text DEFAULT '7',qty integer DEFAULT 3,total integer GENERATED ALWAYS AS (qty*2) STORED); INSERT INTO "+table.Reference(PostgreSQL)+" (note) VALUES ('12')"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		client.ExecuteResult(cleanup, "DROP SCHEMA "+QuoteIdentifier(schema)+" CASCADE; DROP SCHEMA "+QuoteIdentifier(other)+" CASCADE")
	}()
	session, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	e, err := client.PostgreSQLColumnDefinition(ctx, table, "note")
	if err != nil {
		t.Fatal(err)
	}
	e.Name, e.Type, e.Default, e.Using, e.NotNull, e.Comment = "label", "integer", "7", `"note"::integer`, true, "Owner's note"
	if err = session.AlterPostgreSQLColumn(ctx, table, e); err != nil {
		t.Fatal(err)
	}
	current, err := client.PostgreSQLColumnDefinition(ctx, table, "label")
	if err != nil || current.Type != "integer" || !current.NotNull || current.Default != "7" || current.Comment != "Owner's note" {
		t.Fatalf("edit did not persist: %+v %v", current, err)
	}
	rows, err := client.Query(ctx, "SELECT label,total FROM "+table.Reference(PostgreSQL))
	if err != nil || rows.Rows[0][0] != "12" || rows.Rows[0][1] != "6" {
		t.Fatalf("row data/generation changed unexpectedly: %+v %v", rows, err)
	}
	// A late rename failure must roll back the earlier type/default changes.
	failing := current
	failing.Name, failing.Type, failing.Default = "id", "text", "'7'"
	if err = session.AlterPostgreSQLColumn(ctx, table, failing); err == nil {
		t.Fatal("duplicate name accepted")
	}
	after, err := client.PostgreSQLColumnDefinition(ctx, table, "label")
	if err != nil || after.Type != "integer" || after.Default != "7" {
		t.Fatalf("partial edit escaped rollback: %+v %v", after, err)
	}
	if _, err = client.ExecuteResult(ctx, "COMMENT ON COLUMN "+table.Reference(PostgreSQL)+".label IS 'external change'"); err != nil {
		t.Fatal(err)
	}
	current.Comment = "overwrite"
	if err = session.AlterPostgreSQLColumn(ctx, table, current); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("stale column overwrite accepted: %v", err)
	}
	identity, err := client.PostgreSQLColumnDefinition(ctx, table, "id")
	if err != nil {
		t.Fatal(err)
	}
	identity.Type = "bigint"
	if err = session.AlterPostgreSQLColumn(ctx, table, identity); err == nil {
		t.Fatal("identity type modification accepted")
	}
	generated, err := client.PostgreSQLColumnDefinition(ctx, table, "total")
	if err != nil {
		t.Fatal(err)
	}
	generated.Name, generated.Comment = "computed_total", "Generated"
	if err = session.AlterPostgreSQLColumn(ctx, table, generated); err != nil {
		t.Fatal(err)
	}
	if _, err = session.ExecuteResult(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err = session.AlterPostgreSQLColumn(ctx, table, generated); err == nil || !strings.Contains(err.Error(), "existing transaction") {
		t.Fatalf("existing transaction was accepted: %v", err)
	}
	if _, err = session.ExecuteResult(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	properties, err := client.PostgreSQLProperties(ctx, table)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ExecuteResult(ctx, "COMMENT ON TABLE "+table.Reference(PostgreSQL)+" IS 'external table change'"); err != nil {
		t.Fatal(err)
	}
	if err = session.AlterPostgreSQLTable(ctx, table, properties, properties); err == nil {
		t.Fatal("stale table overwrite accepted")
	}
	properties, err = client.PostgreSQLProperties(ctx, table)
	if err != nil {
		t.Fatal(err)
	}
	edit := properties
	edit.Name, edit.ReferenceSchema, edit.Expression, edit.RowSecurity = "renamed", other, "Updated table", true
	if err = session.AlterPostgreSQLTable(ctx, table, properties, edit); err != nil {
		t.Fatal(err)
	}
	updated := Table{Schema: other, Name: "renamed"}
	result, err := client.PostgreSQLProperties(ctx, updated)
	if err != nil || result.ObjectID != properties.ObjectID || result.Expression != "Updated table" || !result.RowSecurity {
		t.Fatalf("table edit failed: %+v %v", result, err)
	}
}
