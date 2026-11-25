package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestReferenceExplorerTree(t *testing.T) {
	a := &App{connection: "Application PostgreSQL", explorerWidth: 400, explorerCatalog: database.ExplorerCatalog{Engine: database.PostgreSQL, Database: "datagrip", FetchedAt: time.Now()}}
	for _, g := range []struct{ scope, name, object string }{{"schema:public", "routines", "find_account(integer)"}, {"schema:public", "aggregates", "total_amount(numeric)"}, {"schema:public", "sequences", "accounts_id_seq"}, {"schema:public", "object types", "order_status"}, {"database", "access methods", "btree"}, {"database", "casts", "integer → bigint"}, {"database", "extensions", "plpgsql"}, {"database", "languages", "sql"}, {"virtual", "virtual views", "sessions"}, {"server", "roles", "analyst"}, {"server", "tablespaces", "pg_default"}} {
		a.explorerCatalog.Groups = append(a.explorerCatalog.Groups, database.ExplorerGroup{Scope: g.scope, Name: g.name, State: "loaded", Items: []database.Definition{{Name: g.object, Properties: []database.Property{{Name: "Owner", Value: "postgres"}}}}})
	}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{{Schema: "public", Name: "accounts", Kind: "Tables"}, {Schema: "public", Name: "orders", Kind: "Tables"}, {Schema: "public", Name: "active_accounts", Kind: "Views"}}}}, true)
	a.treeClosed["group:"+a.schemas[0].ID()+":Tables"] = true
	a.treeClosed["group:"+a.schemas[0].ID()+":Views"] = true
	a.treeClosed["extra:virtual:virtual views"] = false
	a.treeClosed["extra:server:tablespaces"] = false
	a.selectedTree = "database:datagrip"
	v := ui.NewTester(func(c *ui.Context) {
		theme := *ui.DarkTheme()
		theme.Font = "Inter"
		theme.FontSize = 13
		theme.Spacing = 3
		c.SetTheme(&theme)
		a.sidebar(c, paletteOf(c))
	}, 400, 950)
	for _, text := range []string{"Application PostgreSQL", "datagrip", "public", "tables", "views", "routines", "aggregates", "sequences", "object types", "Database Objects", "access methods", "casts", "extensions", "languages", "virtual views", "sessions", "Server Objects", "roles", "tablespaces", "pg_default"} {
		if !v.HasText(text) {
			t.Fatalf("missing tree category %s", text)
		}
	}
	if err := v.Click("Toggle datagrip"); err != nil {
		t.Fatal(err)
	}
	if v.HasText("public") || !v.HasText("Server Objects") {
		t.Fatal("database collapse must preserve server objects")
	}
	if err := v.Click("Toggle datagrip"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Toggle routines"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("find_account(integer)") {
		t.Fatal("routine expansion lost catalog objects")
	}
	if err := v.Click("Toggle routines"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "datagrip-tree.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
