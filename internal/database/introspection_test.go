package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bqapi "google.golang.org/api/bigquery/v2"
)

type schemaFixture struct {
	catalogCalls, schemaCalls, tableCalls int
	schemas                               []Schema
	tables                                []Table
	metadata                              TableMetadata
	fail                                  bool
}

func (f *schemaFixture) Catalog(context.Context) ([]Schema, error) {
	f.catalogCalls++
	if f.fail {
		return nil, errors.New("denied")
	}
	return cloneSchemaValue(f.schemas), nil
}
func (f *schemaFixture) SchemaTables(context.Context, Schema) ([]Table, error) {
	f.schemaCalls++
	if f.fail {
		return nil, errors.New("denied")
	}
	return cloneSchemaValue(f.tables), nil
}
func (f *schemaFixture) TableMetadata(context.Context, Table) (TableMetadata, error) {
	f.tableCalls++
	if f.fail {
		return TableMetadata{}, errors.New("denied")
	}
	return cloneSchemaValue(f.metadata), nil
}

func TestPersistentIntrospectionCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c := &Client{config: Config{Engine: PostgreSQL, Host: "localhost", Database: "app", User: "alice", Password: "secret-never-cache"}}
	table := Table{Schema: "public", Name: "events", Kind: "Tables"}
	schema := Schema{Name: "public"}
	f := &schemaFixture{schemas: []Schema{schema}, tables: []Table{table}, metadata: TableMetadata{Kind: "Partitioned tables", Partition: PartitionSpec{Strategy: "RANGE", Field: "id"}, Columns: []Column{{Name: "profile", Type: "RECORD", Fields: []Column{{Name: "city", Type: "STRING"}}}}}}
	p := NewIntrospector(c, dir)
	p.source = f
	if _, e := p.Catalog(ctx, false); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Schema(ctx, schema, false); e != nil {
		t.Fatal(e)
	}
	first, e := p.Table(ctx, table, false)
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(p.path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "secret-never-cache") {
		t.Fatal("credential leaked to cache")
	}
	reopened := NewIntrospector(c, dir)
	reopened.source = f
	schemas, e := reopened.Catalog(ctx, false)
	if e != nil || !schemas[0].FromCache || !schemas[0].Loaded || schemas[0].Tables[0].Kind != "Partitioned tables" {
		t.Fatalf("catalog cache: %+v %v", schemas, e)
	}
	m, e := reopened.Table(ctx, table, false)
	if e != nil || !m.FromCache || m.Columns[0].Fields[0].Name != "city" || !m.FetchedAt.Equal(first.FetchedAt) {
		t.Fatalf("nested cache: %+v %v", m, e)
	}
	m.Columns[0].Fields[0].Name = "mutated"
	again, _ := reopened.Table(ctx, table, false)
	if again.Columns[0].Fields[0].Name != "city" {
		t.Fatal("UI value aliases cache memory")
	}
	if f.catalogCalls != 1 || f.schemaCalls != 1 || f.tableCalls != 1 {
		t.Fatal("cache hit performed catalog IO")
	}
	f.metadata.Columns = []Column{{Name: "new_field", Type: "INT64"}}
	if _, e = reopened.Table(ctx, table, true); e != nil {
		t.Fatal(e)
	}
	if f.tableCalls != 2 {
		t.Fatal("force refresh used cache")
	}
	f.fail = true
	if _, e = reopened.Table(ctx, table, true); e == nil {
		t.Fatal("permission error hidden")
	}
	f.fail = false
	m, _ = reopened.Table(ctx, table, false)
	if m.Columns[0].Name != "new_field" {
		t.Fatal("failed refresh replaced last good snapshot")
	}
	f.tables = nil
	if _, e = reopened.Schema(ctx, schema, true); e != nil {
		t.Fatal(e)
	}
	if len(reopened.snapshot.Metadata) != 0 {
		t.Fatal("schema refresh retained removed table")
	}
	if _, e = reopened.Catalog(ctx, true); e != nil {
		t.Fatal(e)
	}
	if reopened.snapshot.Schemas[0].Loaded {
		t.Fatal("root refresh must preserve lazy dataset loading")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before := f.catalogCalls
	if _, e = reopened.Catalog(cancelled, true); e == nil || before != f.catalogCalls {
		t.Fatal("cancelled discovery accessed source")
	}
	other := &Client{config: Config{Engine: PostgreSQL, Host: "localhost", Database: "app", User: "bob"}}
	if NewIntrospector(other, dir).path == p.path {
		t.Fatal("users share cache scope")
	}
}

func TestCacheExpiryCorruptionAndWriteFailure(t *testing.T) {
	c := &Client{config: Config{Engine: PostgreSQL, Host: "host", Database: "db", User: "user"}}
	f := &schemaFixture{schemas: []Schema{{Name: "public"}}, metadata: TableMetadata{Columns: []Column{{Name: "x"}}}}
	p := NewIntrospector(c, t.TempDir())
	p.source = f
	ctx := context.Background()
	if _, err := p.Catalog(ctx, false); err != nil {
		t.Fatal(err)
	}
	p.snapshot.CatalogAt = time.Now().Add(-31 * 24 * time.Hour)
	if _, err := p.Catalog(ctx, false); err != nil {
		t.Fatal(err)
	}
	if f.catalogCalls != 2 {
		t.Fatal("expired catalog not reloaded")
	}
	if err := os.WriteFile(p.path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	p = NewIntrospector(c, filepath.Dir(p.path))
	p.source = f
	if p.Warning() == "" {
		t.Fatal("corrupt cache was silent")
	}
	if _, err := p.Catalog(ctx, false); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p = NewIntrospector(c, parent)
	p.source = f
	if _, err := p.Catalog(ctx, false); err != nil {
		t.Fatal("cache failure must not fail live discovery")
	}
	if p.Warning() == "" {
		t.Fatal("cache write failure not reported")
	}
}

func TestSQLiteAttachedSchemasAndRichColumns(t *testing.T) {
	c := demo(t)
	ctx := context.Background()
	_, err := c.Execute(ctx, `ATTACH ':memory:' AS extra; CREATE TABLE extra.parent (a INTEGER, b INTEGER, PRIMARY KEY(a,b)) WITHOUT ROWID;
 CREATE TABLE extra.child (a INTEGER,b INTEGER, label TEXT GENERATED ALWAYS AS (a || '-' || b) STORED, FOREIGN KEY(a,b) REFERENCES parent(a,b));
 CREATE INDEX extra.child_ab ON child(a,b); CREATE TRIGGER extra.child_insert AFTER INSERT ON child BEGIN SELECT 1; END;
 CREATE TEMP TABLE session_data (id INTEGER);`)
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := c.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, s := range schemas {
		found[s.Name] = true
	}
	if !found["main"] || !found["extra"] || !found["temp"] {
		t.Fatalf("session schemas missing: %+v", schemas)
	}
	m, err := c.TableMetadata(ctx, Table{Schema: "extra", Name: "child", Kind: "Tables"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Columns) != 3 || m.Columns[2].Generated != "STORED" || len(m.Indexes) != 1 || len(m.Triggers) != 1 || m.Columns[0].References[0].Column != "a" || m.Columns[1].References[0].Column != "b" {
		t.Fatalf("rich SQLite metadata: %+v", m)
	}
	// PK columns of ordinary rowid tables can be nullable in SQLite.
	if _, err = c.Execute(ctx, `CREATE TABLE nullable_key (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	m, err = c.TableMetadata(ctx, Table{Schema: "main", Name: "nullable_key"})
	if err != nil || m.Columns[0].Nullable != "YES" {
		t.Fatalf("SQLite PK nullability: %+v %v", m, err)
	}
}

func TestNestedSchemaBounds(t *testing.T) {
	f := &bqapi.TableFieldSchema{Name: "root", Type: "RECORD"}
	root := f
	for i := 0; i < 34; i++ {
		child := &bqapi.TableFieldSchema{Name: "child", Type: "RECORD"}
		f.Fields = []*bqapi.TableFieldSchema{child}
		f = child
	}
	count := 0
	if _, err := bigQueryColumns([]*bqapi.TableFieldSchema{root}, 0, &count); err == nil {
		t.Fatal("depth bound ignored")
	}
}
