package app

import (
	"fmt"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestExplorerSearchShowsPendingDatasetUntilNamesArrive(t *testing.T) {
	schema := database.Schema{Project: "warehouse", Name: "raw"}
	a := &App{connection: "Warehouse", explorerWidth: 360, filter: "orders", win: new(mygo.Window)}
	a.applyCatalog([]database.Schema{schema}, true)
	a.schemaLoading = map[string]*uint64{schema.ID(): &a.metadataVersion}
	v := ui.NewTester(func(c *ui.Context) {
		a.schemaTrees(c, paletteOf(c), 0, schema.Project)
	}, 360, 700)
	if v.HasText("No matching datasets or schemas") {
		t.Fatal("an unloaded dataset must not produce a definitive empty result")
	}
	schema.Loaded = true
	schema.Tables = []database.Table{{Project: schema.Project, Schema: schema.Name, Name: "orders"}}
	delete(a.schemaLoading, schema.ID())
	a.applyCatalog([]database.Schema{schema}, false)
	v.Frame()
	if explorerTableRows(v) != 1 || !a.treeClosed["schema:"+schema.ID()] {
		t.Fatal("a discovered object must be searchable without manually opening its dataset")
	}
}

func largeExplorerSchema(project, name string, count int) database.Schema {
	schema := database.Schema{Project: project, Name: name, Loaded: true}
	for i := range count {
		schema.Tables = append(schema.Tables, database.Table{Project: project, Schema: name, Name: fmt.Sprintf("table_%04d", i), Kind: "Tables"})
	}
	return schema
}

func explorerTableRows(v *ui.Tester) int {
	count := 0
	for _, text := range v.Texts() {
		if strings.HasPrefix(text, "Inspect table ") {
			count++
		}
	}
	return count
}

func TestExplorerLargeBranchScopedSearch(t *testing.T) {
	schema := largeExplorerSchema("", "public", 3000)
	a := &App{connection: "Warehouse", explorerWidth: 360, hideSearch: true}
	a.applyCatalog([]database.Schema{schema}, true)
	v := ui.NewTester(func(c *ui.Context) { a.sidebar(c, paletteOf(c)) }, 360, 700)
	if got := explorerTableRows(v); got != 100 {
		t.Fatalf("large branch built %d table rows, want 100", got)
	}
	if !v.HasText("Showing 100 of 3000") {
		t.Fatal("missing accurate truncation count")
	}
	if _, ok := v.Find("Inspect table public.table_0100"); ok {
		t.Fatal("the 101st table must not be built")
	}
	v.Scroll(180, 600, 0, 10000)
	if err := v.Click("Search for more in public Tables"); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	input, ok := v.Find("Search within schema or dataset")
	if !ok || input.Y < 0 || input.Y+input.H > 700 {
		t.Fatalf("scoped search was not scrolled into view: %+v", input)
	}
	v.Type("TABLE_2999")
	if a.explorerSearch.query != "TABLE_2999" {
		t.Fatal("scoped search did not receive keyboard focus")
	}
	if got := explorerTableRows(v); got != 1 {
		t.Fatalf("scoped search built %d rows, want 1", got)
	}
	if _, ok := v.Find("Inspect table public.table_2999"); !ok {
		t.Fatal("scoped search did not find a table beyond the display limit")
	}
	if v.HasText("Expand the table to load metadata") || a.busy || len(a.metadataLoading) != 0 {
		t.Fatal("name search must not expand or fetch table metadata")
	}
	a.explorerSearch.query = "table_"
	v.Frame()
	if explorerTableRows(v) != 100 || !v.HasText("Refine search to see other matches") {
		t.Fatal("broad search must remain bounded and explain how to find other matches")
	}
	a.explorerSearch.query = "does_not_exist"
	v.Frame()
	if explorerTableRows(v) != 0 || !v.HasText("No matching objects") {
		t.Fatal("missing empty scoped-search state")
	}
	if err := v.Click("Close schema search"); err != nil {
		t.Fatal(err)
	}
	if a.explorerSearch.schemaID != "" || explorerTableRows(v) != 100 {
		t.Fatal("closing scoped search did not restore the bounded tree")
	}
}

func TestExplorerObjectLimitBoundary(t *testing.T) {
	for _, count := range []int{0, 99, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			a := &App{connection: "Warehouse", explorerWidth: 360}
			a.applyCatalog([]database.Schema{largeExplorerSchema("", "public", count)}, true)
			v := ui.NewTester(func(c *ui.Context) { a.sidebar(c, paletteOf(c)) }, 360, 700)
			if got := explorerTableRows(v); got != min(count, 100) {
				t.Fatalf("built %d rows for %d objects", got, count)
			}
			_, more := v.Find("Search for more in public Tables")
			if more != (count > 100) {
				t.Fatalf("unexpected search-for-more visibility for %d objects", count)
			}
		})
	}
}

func TestBigQueryScopedSearchIsolationAndFlatLimit(t *testing.T) {
	schema := largeExplorerSchema("alpha", "warehouse", 60)
	for i := range 60 {
		schema.Tables = append(schema.Tables, database.Table{Project: "alpha", Schema: schema.Name, Name: fmt.Sprintf("view_%04d", i), Kind: "Views"})
	}
	other := largeExplorerSchema("beta", "warehouse", 1)
	a := &App{connection: "Analytics", explorerWidth: 400, explorerCatalog: database.ExplorerCatalog{Engine: database.BigQuery}}
	a.applyCatalog([]database.Schema{schema, other, {Project: "alpha", Name: "unloaded"}}, true)
	v := ui.NewTester(func(c *ui.Context) { a.sidebar(c, paletteOf(c)) }, 400, 700)
	if got := explorerTableRows(v); got != 101 {
		t.Fatalf("flat dataset must share one 100-object cap across types; built %d rows including other dataset", got)
	}
	if !v.HasText("Showing 100 of 120") {
		t.Fatal("flat dataset truncation must count tables and views together")
	}
	a.treeClosed["schema:"+schema.ID()] = true
	a.treeClosed["project:alpha"] = true
	a.searchSchema(schema)
	a.explorerSearch.query = "view_0059"
	v.Frame()
	if _, ok := v.Find("Inspect table " + schema.Tables[119].Label()); !ok {
		t.Fatal("search must reveal a view beyond the flat dataset cap")
	}
	if _, ok := v.Find("Inspect table " + other.Tables[0].Label()); !ok {
		t.Fatal("scoped search must not filter the same dataset name in another project")
	}
	a.treeClosed["project:beta"] = true
	v.Frame()
	if _, ok := v.Find("Inspect table " + other.Tables[0].Label()); ok {
		t.Fatal("scoped search must not expand another project")
	}
	if a.schemas[2].Loaded || a.busy {
		t.Fatal("scoped search must not load other datasets")
	}
	for _, width := range []int{190, 250, 400} {
		a.explorerWidth = float32(width)
		v.SetSize(width, 700)
		for _, label := range []string{"Close schema search", "Search within schema or dataset"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.X+r.W > float32(width) {
				t.Fatalf("%s overflows a %d-wide sidebar: %+v", label, width, r)
			}
		}
	}
	if err := v.Click("Close schema search"); err != nil {
		t.Fatal(err)
	}
	if !a.treeClosed["schema:"+schema.ID()] || !a.treeClosed["project:alpha"] {
		t.Fatal("search must preserve original ancestor expansion states")
	}
}

func TestSchemaSearchActivatesOwningConnection(t *testing.T) {
	first := &App{connection: "First", hideSearch: true}
	second := first.newConnectionSession()
	second.connection = "Second"
	first.activateConnection()
	schema := largeExplorerSchema("", "public", 101)
	second.searchSchema(schema)
	if first.ensureWorkspace().active != second || second.explorerSearch.schemaID != schema.ID() || first.explorerSearch.schemaID != "" {
		t.Fatal("scoped search must activate and remain on its owning connection")
	}
}
