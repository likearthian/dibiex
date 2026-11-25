package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

type explorerFixture struct {
	schemaFixture
	calls   int
	catalog ExplorerCatalog
}

func (f *explorerFixture) ExplorerObjects(context.Context) (ExplorerCatalog, error) {
	f.calls++
	if f.fail {
		return ExplorerCatalog{}, errors.New("catalog access denied")
	}
	return cloneSchemaValue(f.catalog), nil
}

func TestExplorerCatalogCache(t *testing.T) {
	c := &Client{config: Config{Engine: PostgreSQL, Database: "app"}}
	f := &explorerFixture{catalog: ExplorerCatalog{Version: 1, Engine: PostgreSQL, Database: "app", FetchedAt: time.Now(), Groups: []ExplorerGroup{{Scope: "server", Name: "roles", State: "loaded", Items: []Definition{{Name: "analyst"}}}}}}
	dir := t.TempDir()
	p := NewIntrospector(c, dir)
	p.source = f
	ctx := context.Background()
	if _, err := p.Explorer(ctx, false); err != nil {
		t.Fatal(err)
	}
	reopened := NewIntrospector(c, dir)
	reopened.source = f
	m, err := reopened.Explorer(ctx, false)
	if err != nil || f.calls != 1 || m.Groups[0].Items[0].Name != "analyst" {
		t.Fatalf("cache restart: %+v %v", m, err)
	}
	m.Groups[0].Items[0].Name = "mutated"
	f.fail = true
	if _, err = reopened.Explorer(ctx, true); err == nil {
		t.Fatal("refresh failure suppressed")
	}
	last := NewIntrospector(c, dir)
	last.source = f
	m, err = last.Explorer(ctx, false)
	if err != nil || m.Groups[0].Items[0].Name != "analyst" {
		t.Fatal("failed refresh damaged last good catalog")
	}
	f.fail = false
	last.Invalidate()
	if _, err = last.Explorer(ctx, false); err != nil || f.calls != 3 {
		t.Fatal("DDL invalidation did not expire extended catalog")
	}
	last.snapshot.Explorer.Version = 0
	if _, err = last.Explorer(ctx, false); err != nil || f.calls != 4 {
		t.Fatal("old sequence metadata format was not rediscovered")
	}
}
