package database

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLConfigValidation(t *testing.T) {
	valid := Config{Engine: MySQL, Host: "localhost", Port: "3306", Database: "app", User: "root", SSLMode: "preferred"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing host", func(c *Config) { c.Host = "" }},
		{"missing database", func(c *Config) { c.Database = " " }},
		{"missing user", func(c *Config) { c.User = "" }},
		{"invalid port", func(c *Config) { c.Port = "mysql" }},
		{"out of range port", func(c *Config) { c.Port = "70000" }},
		{"postgres TLS mode", func(c *Config) { c.SSLMode = "verify-full" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("settings accepted: %+v", cfg)
			}
		})
	}
	if got := valid.Label(); got != "app @ localhost" {
		t.Fatalf("label = %q", got)
	}
}

func TestMySQLDSN(t *testing.T) {
	cfg := Config{Engine: MySQL, Host: "db.internal", Port: "3307", Database: "shop", User: "app", Password: "s3cret", SSLMode: "preferred"}
	parsed, err := mysql.ParseDSN(cfg.mysqlDSN())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != "app" || parsed.Passwd != "s3cret" || parsed.Addr != "db.internal:3307" || parsed.DBName != "shop" {
		t.Fatalf("unexpected DSN parts: %+v", parsed)
	}
	if parsed.TLSConfig != "preferred" || !parsed.MultiStatements {
		t.Fatalf("unexpected DSN options: %+v", parsed)
	}
	if !strings.Contains(cfg.mysqlDSN(), "charset=utf8mb4") {
		t.Fatalf("DSN lost the utf8mb4 charset: %q", cfg.mysqlDSN())
	}
}

func TestMySQLQuoting(t *testing.T) {
	table := Table{Schema: "shop", Name: "order`items"}
	if got, want := table.Reference(MySQL), "`shop`.`order``items`"; got != want {
		t.Fatalf("reference = %q, want %q", got, want)
	}
	if got, want := table.Preview(MySQL), "SELECT * FROM `shop`.`order``items` LIMIT 1000;"; got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
	if got, want := QuoteIdentifierFor(MySQL, "we`ird"), "`we``ird`"; got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
	if got, want := QuoteIdentifierFor(PostgreSQL, "plain"), `"plain"`; got != want {
		t.Fatalf("PostgreSQL quote = %q, want %q", got, want)
	}
}

func TestMySQLClientIdentity(t *testing.T) {
	c := &Client{config: Config{Engine: MySQL, Database: "shop"}}
	if c.Engine() != MySQL || c.Scope() != "shop" {
		t.Fatalf("engine/scope: %v %q", c.Engine(), c.Scope())
	}
	if !strings.Contains(c.WelcomeQuery(), "DATABASE()") {
		t.Fatalf("welcome query: %q", c.WelcomeQuery())
	}
	if c.Settings().Password != "" {
		t.Fatal("client settings exposed a password")
	}
}

func TestMySQLLiteralEscaping(t *testing.T) {
	if got, want := mysqlLiteral(`a\b'c`), `'a\\b''c'`; got != want {
		t.Fatalf("literal = %q, want %q", got, want)
	}
}

func TestMySQLIndexAndConstraintSQL(t *testing.T) {
	index := mysqlIndex{name: "email", unique: true, columns: []string{"email", "tenant"}}
	if got, want := mysqlIndexSQL(index), "CREATE UNIQUE INDEX `email` (`email`, `tenant`)"; got != want {
		t.Fatalf("index SQL = %q, want %q", got, want)
	}
	primary := mysqlIndex{name: "PRIMARY", unique: true, columns: []string{"id"}}
	if got, want := mysqlIndexSQL(primary), "PRIMARY KEY (`id`)"; got != want {
		t.Fatalf("primary SQL = %q, want %q", got, want)
	}
	foreign := mysqlConstraint{name: "fk_customer", kind: "FOREIGN KEY", columns: []string{"customer_id"}, references: []string{"id"}, referenceSchema: "shop", referenceTable: "customers"}
	if got, want := mysqlConstraintSQL(foreign), "FOREIGN KEY (`customer_id`) REFERENCES `shop`.`customers` (`id`)"; got != want {
		t.Fatalf("foreign SQL = %q, want %q", got, want)
	}
}

func TestDriverCellValue(t *testing.T) {
	binary := []byte{0x00, 0xff}
	if got := driverCellValue(MySQL, []byte("hello"), "VARCHAR"); got != "hello" {
		t.Fatalf("text cell = %#v", got)
	}
	if got := driverCellValue(MySQL, binary, "BLOB"); string(got.([]byte)) != string(binary) {
		t.Fatalf("binary cell was converted: %#v", got)
	}
	if got := driverCellValue(PostgreSQL, []byte(`{"ok":true}`), "JSONB"); got != `{"ok":true}` {
		t.Fatalf("json cell = %#v", got)
	}
}

// Set DBCRAFT_MYSQL_DSN to exercise a real server, e.g.
// root:secret@tcp(127.0.0.1:3306)/shop
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_MYSQL_DSN to test a real MySQL server")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test MySQL DSN")
	}
	if parsed.Net != "tcp" || parsed.Addr == "" {
		t.Fatal("test MySQL DSN must use tcp(host:port)")
	}
	host, port, err := net.SplitHostPort(parsed.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Connect(ctx, Config{Engine: MySQL, Host: host, Port: port, Database: parsed.DBName, User: parsed.User, Password: parsed.Passwd, SSLMode: "preferred"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.Query(ctx, `SELECT 'text' AS t, CAST(NULL AS CHAR) AS e, JSON_OBJECT('ok',true) AS j, x'00ff' AS payload`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "text" || out.Rows[0][1] != "NULL" || !strings.Contains(out.Rows[0][2], `"ok"`) || out.Rows[0][3] != "<BLOB 2 bytes>" {
		t.Fatalf("unexpected values: %+v", out)
	}
	objects, err := c.Objects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) == 0 {
		t.Fatal("test database has no objects to introspect")
	}
	schemas, err := c.Catalog(ctx)
	if err != nil || len(schemas) == 0 {
		t.Fatalf("catalog: %v %+v", err, schemas)
	}
	metadata, err := c.TableMetadata(ctx, objects[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Columns) == 0 || !strings.HasPrefix(metadata.DDL, "CREATE ") || metadata.Sections["columns"].State != "loaded" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if _, err = c.Execute(ctx, "SET @dbcraft_a = 1; SET @dbcraft_b = 2;"); err != nil {
		t.Fatalf("multi-statement script: %v", err)
	}
	session, err := c.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Execute(ctx, "CREATE TEMPORARY TABLE dbcraft_session_only (id INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = session.Query(ctx, "SELECT COUNT(*) FROM dbcraft_session_only"); err != nil {
		t.Fatalf("session lost its temporary table: %v", err)
	}
	if _, err = c.Query(ctx, "SELECT COUNT(*) FROM dbcraft_session_only"); err == nil {
		t.Fatal("temporary table leaked into the catalog session")
	}
	exported := 0
	err = c.StreamExportQuery(ctx, "SELECT 1 AS n UNION ALL SELECT 2", func(_ []string, next func() ([]string, error)) error {
		for {
			row, e := next()
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			exported++
			_ = row
		}
	})
	if err != nil || exported != 2 {
		t.Fatalf("export: %d %v", exported, err)
	}
}
