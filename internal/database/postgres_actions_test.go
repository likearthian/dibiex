package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLActionSQL(t *testing.T) {
	table := Table{Schema: `a"schema`, Name: "items"}
	for _, tc := range []struct {
		action TableAction
		want   string
	}{
		{TableAction{Kind: "Drop", Cascade: true}, `DROP TABLE "a""schema"."items" CASCADE;`},
		{TableAction{Kind: "Truncate", RestartIdentity: true, Cascade: true}, `TRUNCATE TABLE "a""schema"."items" RESTART IDENTITY CASCADE;`},
		{TableAction{Kind: "Column", Name: `a"col`, Type: "varchar(80)", NotNull: true}, `ADD COLUMN "a""col" varchar(80) NOT NULL`},
		{TableAction{Kind: "Index", Name: "ix", Columns: "id, tenant_id", Unique: true}, `CREATE UNIQUE INDEX "ix" ON`},
		{TableAction{Kind: "Foreign key", Name: "fk", Columns: "parent", ReferenceSchema: "public", ReferenceTable: "parent", ReferenceColumns: "id"}, `FOREIGN KEY ("parent") REFERENCES "public"."parent" ("id")`},
		{TableAction{Kind: "Maintenance", Command: "VACUUM (ANALYZE)"}, `VACUUM (ANALYZE) "a""schema"."items";`},
		{TableAction{Kind: "Trigger", Name: "before_save", Timing: "BEFORE", Event: "INSERT", ReferenceSchema: "public", Function: "on_save"}, `EXECUTE FUNCTION "public"."on_save"()`},
		{TableAction{Kind: "Properties", Name: "renamed", Expression: "Owner's table; -- comment", RowSecurity: true}, `COMMENT ON TABLE "a""schema"."renamed" IS E'Owner''s table; -- comment'`},
	} {
		sql, err := tc.action.SQL(table)
		if err != nil || !strings.Contains(sql, tc.want) {
			t.Fatalf("%s: %q %v", tc.action.Kind, sql, err)
		}
	}
	for _, action := range []TableAction{{Kind: "Column", Name: "x", Type: "integer; DELETE FROM t"}, {Kind: "Index", Name: "x", Columns: ""}, {Kind: "Check", Name: "x", Expression: "1=1; DROP TABLE x"}, {Kind: "Maintenance", Command: "VACUUM; DROP TABLE x"}, {Kind: "Properties", Name: "x", ForceRowSecurity: true}} {
		if _, err := action.SQL(table); err == nil {
			t.Fatalf("unsafe/invalid action accepted: %+v", action)
		}
	}
}

func TestPostgreSQLToolArguments(t *testing.T) {
	cfg := Config{Engine: PostgreSQL, Host: "localhost", Port: "5432", Database: "db/name", User: "person", Password: "not-in-args", SSLMode: "verify-full"}
	table := Table{Schema: "public", Name: `a"table`}
	name, args, err := PGToolArguments(cfg, table, PGToolOptions{Plain: true, SchemaOnly: true})
	joined := strings.Join(args, " ")
	if err != nil || name != "pg_dump" || strings.Contains(joined, cfg.Password) || !strings.Contains(joined, "db%2Fname") || !strings.Contains(joined, "--format=plain") || !strings.Contains(joined, "--schema-only") {
		t.Fatalf("incorrect dump arguments: %q %v", joined, err)
	}
	name, args, err = PGToolArguments(cfg, table, PGToolOptions{Restore: true, DataOnly: true, Path: "a file.dump"})
	joined = strings.Join(args, " ")
	if err != nil || name != "pg_restore" || !strings.Contains(joined, "--single-transaction") || !strings.Contains(joined, "--table=a\"table") || !strings.Contains(joined, "--data-only") {
		t.Fatalf("incorrect restore arguments: %q %v", joined, err)
	}
	writer := &LimitedWriter{Writer: &bytes.Buffer{}, Remaining: 3}
	if _, err := writer.Write([]byte("four")); err == nil {
		t.Fatal("transfer bound not enforced")
	}
}

func TestPostgreSQLDataEditorAndCSVIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_TEST_POSTGRES_DSN to test PostgreSQL data editing and COPY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test connection")
	}
	db.SetMaxOpenConns(17)
	base, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	client := &Client{db: db, session: base, config: Config{Engine: PostgreSQL}}
	defer client.Close()
	schema := "dbcraft_actions_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	table := Table{Schema: schema, Name: "records"}
	if _, err = client.ExecuteResult(ctx, "CREATE SCHEMA "+QuoteIdentifier(schema)+"; CREATE TABLE "+table.Reference(PostgreSQL)+" (id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY, note text, qty integer DEFAULT 7)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		client.ExecuteResult(cleanup, "DROP SCHEMA "+QuoteIdentifier(schema)+" CASCADE")
	}()
	session, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cols, err := session.PostgreSQLColumns(ctx, table)
	if err != nil {
		t.Fatal(err)
	}
	if err = session.ChangePostgreSQLRow(ctx, table, cols, nil, []TableCell{{}, {Text: "NULL", Include: true}, {}}, "Insert"); err != nil {
		t.Fatal(err)
	}
	data, err := session.ReadPostgreSQLTable(ctx, table, 100, "", false)
	if err != nil || len(data.Rows) != 1 || data.Rows[0][1].Null || data.Rows[0][1].Text != "NULL" || data.Rows[0][2].Text != "7" {
		t.Fatalf("insert/default/NULL semantics: %+v %v", data, err)
	}
	original := append([]TableCell(nil), data.Rows[0]...)
	values := append([]TableCell(nil), original...)
	values[1].Null = true
	if err = session.ChangePostgreSQLRow(ctx, table, cols, original, values, "Update"); err != nil {
		t.Fatal(err)
	}
	if err = session.ChangePostgreSQLRow(ctx, table, cols, original, values, "Delete"); err == nil {
		t.Fatal("stale data was deleted")
	}
	var output bytes.Buffer
	if _, err = session.CopyPostgreSQLCSV(ctx, table, "", true, ",", nil, &output); err != nil {
		t.Fatal(err)
	}
	parsed, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil || len(parsed) != 2 {
		t.Fatalf("COPY export invalid: %q %v", output.String(), err)
	}
	if _, err = session.CopyPostgreSQLCSV(ctx, table, "note,qty", false, ",", strings.NewReader("\"hello, world\",3\n"), nil); err != nil {
		t.Fatal(err)
	}
	data, err = session.ReadPostgreSQLTable(ctx, table, 100, "", true)
	if err != nil || data.Rows[0][1].Text != "hello, world" {
		t.Fatalf("CSV import or descending data view failed: %+v %v", data, err)
	}
}
