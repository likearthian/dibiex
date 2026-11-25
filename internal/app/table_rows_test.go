package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestTableAnnotations(t *testing.T) {
	for _, tc := range []struct{ section, sql, want string }{{"keys", `PRIMARY KEY (actor_id)`, "(actor_id)"}, {"indexes", `CREATE UNIQUE INDEX "name(with brackets)" ON actor USING btree (actor_id)`, "(actor_id) UNIQUE"}, {"indexes", `CREATE INDEX idx ON actor (lower(last_name), first_name DESC) WHERE first_name <> 'a(b)'`, "(lower(last_name), first_name DESC)"}, {"triggers", `CREATE TRIGGER last_updated BEFORE UPDATE ON actor FOR EACH ROW EXECUTE FUNCTION last_updated()`, "before update → last_updated"}} {
		if got := definitionAnnotation(database.Definition{SQL: tc.sql}, tc.section); got != tc.want {
			t.Fatalf("%s annotation: %q, want %q", tc.section, got, tc.want)
		}
	}
	if columnAnnotation(database.Column{Type: "integer", Default: "nextval('actor_id_seq')"}) != "integer = nextval('actor_id_seq')" {
		t.Fatal("column default annotation lost")
	}
	if !isKeyDefinition(database.Definition{Kind: "p"}) || isKeyDefinition(database.Definition{Kind: "CHECK"}) {
		t.Fatal("key classification mixes other constraints")
	}
	index := database.Definition{Properties: []database.Property{{Name: "Unique", Value: "true"}, {Name: "Columns / expressions", Children: []database.Property{{Name: "1", Children: []database.Property{{Name: "name", Value: "actor_id"}, {Name: "key", Value: "1"}}}}}}}
	if got := definitionAnnotation(index, "indexes"); got != "(actor_id) UNIQUE" {
		t.Fatalf("automatic index details: %s", got)
	}
}

func TestReferenceTableTree(t *testing.T) {
	table := database.Table{Schema: "public", Name: "actor", Kind: "Tables"}
	a := &App{connection: "Application PostgreSQL", explorerWidth: 520, hideSearch: true, explorerCatalog: database.ExplorerCatalog{Engine: database.PostgreSQL, Database: "app", FetchedAt: time.Now()}}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{table}}}, true)
	a.treeClosed["table:"+table.ID()] = false
	m := database.TableMetadata{FetchedAt: time.Now(), Columns: []database.Column{{Name: "actor_id", Type: "integer", PrimaryKey: true, Default: "nextval('actor_actor_id_seq'::regclass)"}, {Name: "first_name", Type: "varchar(45)"}, {Name: "last_name", Type: "varchar(45)"}, {Name: "last_update", Type: "timestamp", Default: "now()"}}, Constraints: []database.Definition{{Name: "actor_pkey", Kind: "p", SQL: "PRIMARY KEY (actor_id)"}}, Indexes: []database.Definition{{Name: "actor_pkey", Kind: "INDEX", SQL: "CREATE UNIQUE INDEX actor_pkey ON public.actor USING btree (actor_id)"}, {Name: "idx_actor_last_name", Kind: "INDEX", SQL: "CREATE INDEX idx_actor_last_name ON public.actor USING btree (last_name)"}}, Triggers: []database.Definition{{Name: "last_updated", Kind: "TRIGGER", SQL: "CREATE TRIGGER last_updated BEFORE UPDATE ON actor FOR EACH ROW EXECUTE FUNCTION last_updated()"}}, Sections: map[string]database.MetadataSection{}}
	for _, s := range []string{"columns", "constraints", "indexes", "triggers", "partitions", "policies", "privileges", "rules"} {
		m.Sections[s] = database.MetadataSection{State: "loaded"}
	}
	a.metadata = map[string]database.TableMetadata{table.ID(): m}
	a.tableSectionsOpen = map[string]bool{}
	for _, s := range []string{"columns", "keys", "indexes", "triggers"} {
		a.tableSectionsOpen[sectionKey(table, s)] = true
	}
	v := ui.NewTester(func(c *ui.Context) {
		theme := *ui.DarkTheme()
		theme.Font = "Inter"
		theme.FontSize = 13
		c.SetTheme(&theme)
		ui.Column(c).Fill().Background(paletteOf(c).panel).Children(func() { a.tableDetailsTree(c, paletteOf(c), table) })
	}, 520, 540)
	for _, label := range []string{"Toggle Columns for public.actor", "Toggle Keys for public.actor", "Toggle Indexes for public.actor", "Toggle Triggers for public.actor", "Inspect keys actor_pkey", "Inspect indexes idx_actor_last_name", "Inspect triggers last_updated", "Column details for last_update"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing reference table item: %s", label)
		}
	}
	if _, ok := v.Find("Toggle Constraints for public.actor"); ok {
		t.Fatal("key duplicated under constraints")
	}
	if err := v.Click("Toggle Keys for public.actor"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Inspect keys actor_pkey"); ok {
		t.Fatal("keys did not collapse")
	}
	if _, ok := v.Find("Inspect indexes actor_pkey"); !ok {
		t.Fatal("collapsing keys hid indexes")
	}
	if err := v.Click("Toggle Keys for public.actor"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "table-tree-reference.png"))
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
