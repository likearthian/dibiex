package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSQLiteTableProperties(t *testing.T) {
	c := demo(t)
	ctx := context.Background()
	_, err := c.Execute(ctx, `CREATE TABLE parent_properties(a TEXT,b INTEGER,PRIMARY KEY(a,b)) WITHOUT ROWID;
 CREATE TABLE rich_properties(a TEXT NOT NULL,b INTEGER,quantity INTEGER DEFAULT 1,
 derived INTEGER GENERATED ALWAYS AS (quantity*2) STORED,
 CONSTRAINT composite_key PRIMARY KEY(a,b), UNIQUE(b,quantity),
 CONSTRAINT positive CHECK(quantity > 0 AND length('CHECK(x)') > 0),
 FOREIGN KEY(a,b) REFERENCES parent_properties(a,b) ON DELETE CASCADE) STRICT;
 CREATE INDEX expression_idx ON rich_properties(lower(a) COLLATE NOCASE DESC) WHERE quantity>1;`)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.TableMetadata(ctx, Table{Schema: "main", Name: "rich_properties", Kind: "Tables"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, d := range m.Constraints {
		kinds[d.Kind]++
		if d.Kind == "PRIMARY KEY" && !strings.Contains(d.SQL, `"a", "b"`) {
			t.Fatalf("composite PK order: %+v", d)
		}
	}
	if kinds["PRIMARY KEY"] != 1 || kinds["FOREIGN KEY"] != 1 || kinds["CHECK"] != 1 || kinds["UNIQUE"] != 1 || kinds["NOT NULL"] == 0 {
		t.Fatalf("missing constraints: %+v", m.Constraints)
	}
	if len(m.Indexes) != 3 {
		t.Fatalf("auto indexes missing: %+v", m.Indexes)
	}
	var expression Definition
	for _, d := range m.Indexes {
		if d.Name == "expression_idx" {
			expression = d
		}
	}
	raw, _ := json.Marshal(expression.Properties)
	for _, value := range []string{"NOCASE", "desc", "-2", "Partial"} {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("index property %s missing: %s", value, raw)
		}
	}
	raw, _ = json.Marshal(m.Properties)
	for _, value := range []string{"strict", "hidden", "Source DDL", "Columns"} {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("table property %s missing: %s", value, raw)
		}
	}
	if m.Sections["partitions"].State != "unsupported" || m.Sections["indexes"].State != "loaded" {
		t.Fatalf("section states: %+v", m.Sections)
	}
}

func TestSQLiteCheckParsing(t *testing.T) {
	ddl := `CREATE TABLE "CHECK" ("CONSTRAINT" TEXT DEFAULT 'CHECK(fake)', a INT /* CHECK(fake) */,
 CONSTRAINT "limit check" CHECK((a > 0) AND length(')') > 0),
 b TEXT CHECK (b != 'CHECK(no)')) -- CHECK(fake)`
	defs := sqliteCheckConstraints(ddl)
	if len(defs) != 2 || defs[0].Name != "limit check" || defs[0].SQL != `CHECK((a > 0) AND length(')') > 0)` {
		t.Fatalf("CHECK parsing: %+v", defs)
	}
}

func TestProviderProperties(t *testing.T) {
	props, err := propertiesJSON([]byte(`{"number":9007199254740993,"enabled":false,"password":"secret","options":["password=abc","https://user:pw@example.org/file?access_token=xyz&region=us"],"nested":{"type":"RECORD"}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(props)
	s := string(raw)
	for _, value := range []string{"9007199254740993", "false", "RECORD", "region=us", "redacted"} {
		if !strings.Contains(s, value) {
			t.Fatalf("lost %s: %s", value, s)
		}
	}
	for _, secret := range []string{"secret", "abc", "user:pw", "xyz"} {
		if strings.Contains(s, secret) {
			t.Fatalf("unredacted %s", secret)
		}
	}
	if _, err = propertiesJSON([]byte(strings.Repeat(" ", 16<<20+1))); err == nil {
		t.Fatal("oversized properties accepted")
	}
}

type sectionFixture struct {
	schemaFixture
	calls       int
	sectionFail bool
}

func (f *sectionFixture) TableSection(context.Context, Table, string) ([]Definition, error) {
	f.calls++
	if f.sectionFail {
		return nil, errors.New("permission denied")
	}
	return []Definition{{Name: "search_idx", Kind: "SEARCH INDEX", Properties: []Property{{Name: "status", Value: "ACTIVE"}}}}, nil
}

func TestTableSectionCache(t *testing.T) {
	ctx := context.Background()
	c := &Client{config: Config{Engine: BigQuery, Project: "example"}}
	dir := t.TempDir()
	table := Table{Project: "example", Schema: "dataset", Name: "events"}
	f := &sectionFixture{}
	f.metadata = TableMetadata{FetchedAt: time.Now()}
	f.metadata.section("indexes", "unloaded", "")
	f.metadata.section("triggers", "unsupported", "No triggers")
	p := NewIntrospector(c, dir)
	p.source = f
	m, err := p.Section(ctx, table, "indexes", false)
	if err != nil || len(m.Indexes) != 1 {
		t.Fatalf("load: %+v %v", m, err)
	}
	reopened := NewIntrospector(c, dir)
	reopened.source = f
	m, err = reopened.Section(ctx, table, "indexes", false)
	if err != nil || !m.FromCache || f.calls != 1 {
		t.Fatalf("restart cache: %+v %v calls=%d", m, err, f.calls)
	}
	m.Indexes[0].Name = "mutated"
	f.sectionFail = true
	if _, err = reopened.Section(ctx, table, "indexes", true); err == nil {
		t.Fatal("refresh error suppressed")
	}
	last := NewIntrospector(c, dir)
	last.source = f
	m, err = last.Section(ctx, table, "indexes", false)
	if err != nil || m.Indexes[0].Name != "search_idx" {
		t.Fatalf("failed refresh damaged cache: %+v %v", m, err)
	}
	before := f.calls
	if _, err = last.Section(ctx, table, "triggers", true); err != nil || f.calls != before {
		t.Fatal("unsupported section sent a remote request")
	}
	f.sectionFail = false
	m = last.snapshot.Metadata[table.ID()]
	s := m.Sections["indexes"]
	s.FetchedAt = time.Now().Add(-31 * 24 * time.Hour)
	m.Sections["indexes"] = s
	last.snapshot.Metadata[table.ID()] = m
	if _, err = last.Section(ctx, table, "indexes", false); err != nil || f.calls != before+1 {
		t.Fatal("expired section did not refresh")
	}
}
