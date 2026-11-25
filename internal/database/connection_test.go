package database

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestConnectionValidationAndQuoting(t *testing.T) {
	cfg := Config{Engine: PostgreSQL, Host: "::1", Port: "5432", Database: "db /?#", User: "user@host", Password: "p@ss:/?#'", SSLMode: "verify-full"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed, err := pgx.ParseConfig(cfg.postgresURL())
	if err != nil || parsed.Database != cfg.Database || parsed.User != cfg.User || parsed.Password != cfg.Password || parsed.Host != cfg.Host {
		t.Fatalf("connection settings did not round trip: %v", err)
	}
	if strings.Contains(cfg.Label(), cfg.Password) {
		t.Fatal("password leaked into display label")
	}
	for _, invalid := range []Config{{Engine: PostgreSQL}, {Engine: BigQuery, Project: "p", Dataset: "d"}, {Engine: "unknown"}, {Engine: SQLite}} {
		if invalid.Validate() == nil {
			t.Fatalf("accepted invalid configuration: %+v", invalid)
		}
	}
	table := Table{Schema: `odd.schema`, Name: `a"b`}
	if got := table.Preview(PostgreSQL); got != `SELECT * FROM "odd.schema"."a""b" LIMIT 1000;` {
		t.Fatal(got)
	}
	table = Table{Project: "my-project", Schema: "dataset", Name: "table name"}
	if got := table.Preview(BigQuery); got != "SELECT * FROM `my-project.dataset.table name` LIMIT 1000;" {
		t.Fatal(got)
	}
	if got := formatCell(big.NewRat(1, 8)); got != "0.125" {
		t.Fatal(got)
	}
	if got := formatCell(nil); got != "NULL" {
		t.Fatal(got)
	}
}

func TestPostgreSQLIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_POSTGRES_DSN to test a real PostgreSQL server")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := &Client{db: stdlib.OpenDB(*cfg), config: Config{Engine: PostgreSQL}}
	defer c.Close()
	out, err := c.Query(ctx, `SELECT NULL::text AS empty, decode('0102','hex') AS payload, '{"ok":true}'::jsonb AS document, 123.456::numeric AS amount`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "NULL" || out.Rows[0][1] != "<BLOB 2 bytes>" || !strings.Contains(out.Rows[0][2], `"ok"`) || out.Rows[0][3] != "123.456" {
		t.Fatalf("unexpected values: %+v", out)
	}
	objects, err := c.Objects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("discovered %d objects", len(objects))
	extended, err := c.ExplorerObjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range extended.Groups {
		if g.State == "unavailable" {
			t.Fatalf("extended catalog %s: %s", g.Name, g.Message)
		}
	}
	cancelled, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if _, err = c.Query(cancelled, "SELECT pg_sleep(3)"); err == nil {
		t.Fatal("query ignored cancellation")
	}
}

func TestCatalogPreservesKindsAndEmptySchemas(t *testing.T) {
	c := demo(t)
	if _, err := c.Execute(context.Background(), "CREATE VIEW project_names AS SELECT name FROM projects"); err != nil {
		t.Fatal(err)
	}
	schemas, err := c.Catalog(context.Background())
	if err != nil || len(schemas) != 1 || schemas[0].Name != "main" || !schemas[0].Loaded {
		t.Fatalf("unexpected catalog: %+v %v", schemas, err)
	}
	kinds := map[string]string{}
	for _, table := range schemas[0].Tables {
		kinds[table.Name] = table.Kind
	}
	if kinds["projects"] != "Tables" || kinds["project_names"] != "Views" {
		t.Fatalf("object kinds: %+v", kinds)
	}
	if _, err := c.Execute(context.Background(), "DROP VIEW project_names; DROP TABLE projects"); err != nil {
		t.Fatal(err)
	}
	schemas, err = c.Catalog(context.Background())
	if err != nil || len(schemas) != 1 || len(schemas[0].Tables) != 0 {
		t.Fatalf("empty schema was lost: %+v %v", schemas, err)
	}
	cfg := Config{Engine: BigQuery, Project: "billing-project", MaxBytesBilled: 1073741824}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Label() != "billing-project" {
		t.Fatal("project-only connection label must not contain an empty dataset suffix")
	}
	client := &Client{config: cfg}
	if client.Scope() != "billing-project" {
		t.Fatal("project-only scope must show the project")
	}
}
