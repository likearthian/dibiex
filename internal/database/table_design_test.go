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

func copyDesign(d PostgreSQLTable) PostgreSQLTable {
	d.Columns = append([]PostgreSQLColumn(nil), d.Columns...)
	d.Constraints = append([]PostgreSQLConstraint(nil), d.Constraints...)
	d.Indexes = append([]PostgreSQLIndex(nil), d.Indexes...)
	return d
}

func TestSharedDesignerPlansOnlyChanges(t *testing.T) {
	e := PostgreSQLColumnEdit{Number: 1, Name: "id", Type: "integer", NotNull: true, Original: Column{Name: "id", Type: "integer", Nullable: "NO"}}
	original := PostgreSQLTableDesign{Table: Table{Schema: "public", Name: "items"}, Columns: []PostgreSQLColumnEdit{e}, DatabaseTablespace: "pg_default", Definition: PostgreSQLTable{Schema: "public", Name: "items", Owner: "owner", PartitionMethod: "RANGE", Autovacuum: "Default", Columns: []PostgreSQLColumn{{Number: 1, Name: "id", Type: "integer", NotNull: true, Identity: "None"}}}}
	plan, err := original.Changes(copyDesign(original.Definition))
	if err != nil || len(plan.Statements) != 0 {
		t.Fatalf("unchanged table generated SQL: %+v %v", plan, err)
	}
	d := copyDesign(original.Definition)
	d.Comment = "Updated"
	plan, err = original.Changes(d)
	if err != nil || len(plan.Statements) != 1 || !strings.HasPrefix(plan.Statements[0], "COMMENT ON TABLE") {
		t.Fatalf("unrelated properties changed: %+v %v", plan, err)
	}
	d = copyDesign(original.Definition)
	d.Columns = append(d.Columns, PostgreSQLColumn{Name: "note", Type: "text", Identity: "None"})
	plan, err = original.Changes(d)
	if err != nil || len(plan.Statements) != 1 || !strings.Contains(plan.Statements[0], "ADD COLUMN") || strings.Contains(plan.SQL(), "CREATE TABLE") {
		t.Fatalf("wrong column delta: %+v %v", plan, err)
	}
	d = copyDesign(original.Definition)
	d.Partitioned = true
	if _, err = original.Changes(d); err == nil {
		t.Fatal("partition rewrite accepted")
	}
	d = copyDesign(original.Definition)
	d.Indexes = []PostgreSQLIndex{{Name: "idx", Method: "btree", Columns: "id"}}
	plan, err = original.Changes(d)
	if err != nil || len(plan.Statements) != 1 || !strings.Contains(plan.SQL(), "CREATE INDEX") {
		t.Fatalf("missing index delta: %+v %v", plan, err)
	}
	d = copyDesign(original.Definition)
	d.Constraints = []PostgreSQLConstraint{{Name: "ck", Kind: "CHECK", Expression: "id >= 0"}}
	plan, err = original.Changes(d)
	if err != nil || !strings.Contains(plan.SQL(), "ADD CONSTRAINT") {
		t.Fatalf("missing constraint delta: %+v %v", plan, err)
	}
}

func TestSharedDesignerPostgreSQLIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_TEST_POSTGRES_DSN to test the shared table designer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test connection")
	}
	db.SetMaxOpenConns(17)
	client := &Client{db: db, config: Config{Engine: PostgreSQL}}
	defer client.Close()
	schema := "dbcraft_design_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	target := schema + "_target"
	table := Table{Schema: schema, Name: "items"}
	ref := table.Reference(PostgreSQL)
	_, err = client.ExecuteResult(ctx, "CREATE SCHEMA "+QuoteIdentifier(schema)+"; CREATE SCHEMA "+QuoteIdentifier(target)+"; CREATE TABLE "+ref+` (id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name varchar(40) NOT NULL, qty integer DEFAULT 1, score text DEFAULT '7', total integer GENERATED ALWAYS AS (qty*2) STORED, CONSTRAINT uk_name UNIQUE(name), CONSTRAINT qty_ok CHECK(qty>=0), CONSTRAINT advanced_unique UNIQUE(qty,name) DEFERRABLE INITIALLY DEFERRED) WITH(fillfactor=80,autovacuum_enabled=false,autovacuum_vacuum_scale_factor=0.12); CREATE INDEX qty_idx ON `+ref+`(qty); CREATE INDEX expr_idx ON `+ref+`(lower(name)) INCLUDE(qty); INSERT INTO `+ref+`(name) VALUES('first')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		client.ExecuteResult(cleanup, "DROP SCHEMA "+QuoteIdentifier(schema)+" CASCADE; DROP SCHEMA "+QuoteIdentifier(target)+" CASCADE")
	}()
	snapshot, err := client.LoadPostgreSQLTableDesign(ctx, table)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Definition.Columns) != 5 || len(snapshot.Definition.Constraints) != 4 || len(snapshot.Definition.Indexes) != 5 || snapshot.Definition.Fillfactor != "80" || snapshot.Definition.Autovacuum != "false" {
		t.Fatalf("incomplete live definition: %+v", snapshot.Definition)
	}
	plan, err := snapshot.Changes(copyDesign(snapshot.Definition))
	if err != nil || len(plan.Statements) != 0 {
		t.Fatalf("loading generated ALTER statements: %+v %v", plan, err)
	}
	constraintIDs := map[string]int64{}
	indexIDs := map[string]int64{}
	for _, c := range snapshot.Definition.Constraints {
		constraintIDs[c.Name] = c.ID
		if c.Name == "advanced_unique" && c.ReadOnlyReason == "" {
			t.Fatal("advanced constraint lost its preservation marker")
		}
	}
	for _, i := range snapshot.Definition.Indexes {
		indexIDs[i.Name] = i.ID
		if i.Name == "expr_idx" && i.ReadOnlyReason == "" {
			t.Fatal("advanced index lost its preservation marker")
		}
	}
	d := copyDesign(snapshot.Definition)
	d.Name, d.Schema, d.Comment, d.Fillfactor, d.Autovacuum = "renamed", target, "Edited table", "90", "Default"
	for i := range d.Columns {
		switch d.Columns[i].Name {
		case "name":
			d.Columns[i].Name = "label"
		case "score":
			d.Columns[i].Type = "integer"
			d.Columns[i].Default = "9"
			d.Columns[i].Using = "score::integer"
			d.Columns[i].Comment = "Score"
		}
	}
	d.Columns = append(d.Columns, PostgreSQLColumn{Name: "note", Type: "text", Identity: "None", Comment: "New column"})
	for i := range d.Constraints {
		if d.Constraints[i].Name == "qty_ok" {
			d.Constraints[i].Expression = "qty>=1"
		}
		if d.Constraints[i].Name == "uk_name" {
			d.Constraints[i].Name = "uk_label"
		}
	}
	for i := range d.Indexes {
		if d.Indexes[i].Name == "qty_idx" {
			d.Indexes[i].Name = "quantity_idx"
		}
	}
	d.Indexes = append(d.Indexes, PostgreSQLIndex{Name: "note_idx", Method: "btree", Columns: "note"})
	plan, err = snapshot.Changes(d)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Destructive || strings.Contains(plan.SQL(), "CREATE TABLE") || strings.Contains(plan.SQL(), "DROP INDEX \""+schema+"\".\"expr_idx\"") {
		t.Fatalf("unsafe edit plan: %s", plan.SQL())
	}
	session, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err = session.ApplyPostgreSQLTableDesign(ctx, snapshot, d); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, plan.SQL())
	}
	updatedTable := Table{Schema: target, Name: "renamed"}
	updated, err := client.LoadPostgreSQLTableDesign(ctx, updatedTable)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ObjectID != snapshot.ObjectID || updated.Definition.Fillfactor != "90" || updated.Definition.Autovacuum != "Default" || len(updated.Definition.Columns) != 6 {
		t.Fatalf("incorrect updated design: %+v", updated.Definition)
	}
	for _, c := range updated.Definition.Constraints {
		if c.Name == "advanced_unique" && c.ID != constraintIDs[c.Name] {
			t.Fatal("untouched advanced constraint was replaced")
		}
		if c.Name == "uk_label" && c.ID != constraintIDs["uk_name"] {
			t.Fatal("renaming a constraint replaced it")
		}
	}
	for _, i := range updated.Definition.Indexes {
		if i.Name == "expr_idx" && i.ID != indexIDs["expr_idx"] {
			t.Fatal("untouched advanced index was replaced")
		}
		if i.Name == "quantity_idx" && i.ID != indexIDs["qty_idx"] {
			t.Fatal("renaming an index replaced it")
		}
	}
	rows, err := client.Query(ctx, "SELECT label,score,total FROM "+updatedTable.Reference(PostgreSQL))
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][1] != "7" || rows.Rows[0][2] != "2" {
		t.Fatalf("data was not preserved: %+v %v", rows, err)
	}
	rows, err = client.Query(ctx, "SELECT reloptions::text FROM pg_class WHERE oid="+strconv.FormatInt(snapshot.ObjectID, 10))
	if err != nil || !strings.Contains(rows.Rows[0][0], "autovacuum_vacuum_scale_factor=0.12") {
		t.Fatal("unexposed storage option was lost")
	}
	// A failure after a successful earlier ALTER rolls back the entire draft.
	failing := copyDesign(updated.Definition)
	failing.Comment = "must rollback"
	failing.Columns = append(failing.Columns, PostgreSQLColumn{Name: "must_rollback", Type: "text", Identity: "None"})
	failing.Indexes = append(failing.Indexes, PostgreSQLIndex{Name: "bad_unique", Method: "btree", Columns: "total", Unique: true})
	if _, err = client.ExecuteResult(ctx, "INSERT INTO "+updatedTable.Reference(PostgreSQL)+"(label) VALUES('second')"); err != nil {
		t.Fatal(err)
	}
	if err = session.ApplyPostgreSQLTableDesign(ctx, updated, failing); err == nil {
		t.Fatal("invalid unique index accepted")
	}
	after, err := client.LoadPostgreSQLTableDesign(ctx, updatedTable)
	if err != nil || after.Definition.Comment != "Edited table" || len(after.Definition.Columns) != 6 {
		t.Fatalf("failed draft partially committed: %v", err)
	}
	if _, err = client.ExecuteResult(ctx, "ALTER TABLE "+updatedTable.Reference(PostgreSQL)+" SET (fillfactor=85)"); err != nil {
		t.Fatal(err)
	}
	if err = session.ApplyPostgreSQLTableDesign(ctx, after, copyDesign(after.Definition)); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("stale snapshot accepted: %v", err)
	}
	partition := Table{Schema: schema, Name: "partitioned"}
	if _, err = client.ExecuteResult(ctx, "CREATE TABLE "+partition.Reference(PostgreSQL)+" (id integer) PARTITION BY RANGE(id)"); err != nil {
		t.Fatal(err)
	}
	part, err := client.LoadPostgreSQLTableDesign(ctx, partition)
	if err != nil {
		t.Fatal(err)
	}
	if !part.Definition.Partitioned || part.Definition.PartitionColumns != "id" || part.PartitionSQL == "" {
		t.Fatal("partition definition not loaded")
	}
	if plan, err = part.Changes(copyDesign(part.Definition)); err != nil || len(plan.Statements) != 0 {
		t.Fatalf("partitioned no-op failed: %+v %v", plan, err)
	}
}
