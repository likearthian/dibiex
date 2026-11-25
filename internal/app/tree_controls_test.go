package app

import (
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestTreeDisclosureIsChevronOnly(t *testing.T) {
	table := database.Table{Schema: "public", Name: "events", Kind: "Tables"}
	a := &App{connection: "Workspace", explorerWidth: 350, editorPercent: 60, hideSearch: true, hideResults: true, showFiles: true}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{table}}}, true)
	a.metadata = map[string]database.TableMetadata{table.ID(): {FetchedAt: time.Now(), Columns: []database.Column{{Name: "id", Type: "integer"}}}}
	v := ui.NewTester(a.view, 1472, 884)
	for _, label := range []string{"Workspace", "public", "Tables"} {
		if err := v.Click("Select " + label); err != nil {
			t.Fatal(err)
		}
		if _, ok := v.Find("Inspect table public.events"); !ok {
			t.Fatalf("%s body collapsed tree", label)
		}
	}
	if err := v.Click("Inspect table public.events"); err != nil {
		t.Fatal(err)
	}
	if !a.treeClosed["table:"+table.ID()] || !a.showInspector {
		t.Fatal("table body must select without expanding")
	}
	a.showInspector = false
	v.Frame()
	if err := v.Click("Toggle table public.events"); err != nil {
		t.Fatal(err)
	}
	if a.treeClosed["table:"+table.ID()] {
		t.Fatal("table chevron did not expand")
	}
	if a.showInspector {
		t.Fatal("table chevron changed Inspector selection")
	}
	if err := v.Click("Select Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	if a.tableSectionsOpen[sectionKey(table, "columns")] {
		t.Fatal("folder body expanded columns")
	}
	if err := v.Click("Toggle Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Column id"); !ok {
		t.Fatal("columns chevron did not expand")
	}
	if err := v.Click("Select Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Column id"); !ok {
		t.Fatal("folder body collapsed columns")
	}
	if err := v.Click("Toggle public"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Inspect table public.events"); ok {
		t.Fatal("schema chevron did not collapse")
	}
	if err := v.Click("Select public"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Inspect table public.events"); ok {
		t.Fatal("schema body expanded tree")
	}
	if err := v.Click("Show Files"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Scratches and Consoles"); err != nil {
		t.Fatal(err)
	}
	if a.filesCollapsed {
		t.Fatal("Files folder body collapsed consoles")
	}
	if err := v.Click("Toggle Scratches and Consoles"); err != nil {
		t.Fatal(err)
	}
	if !a.filesCollapsed {
		t.Fatal("Files chevron did not collapse")
	}
}
