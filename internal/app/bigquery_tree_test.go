package app

import (
	"slices"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestBigQueryProjectDatasetTree(t *testing.T) {
	table := database.Table{Project: "analytics-project", Schema: "warehouse", Name: "events", Kind: "Tables"}
	view := database.Table{Project: table.Project, Schema: table.Schema, Name: "recent_events", Kind: "Views"}
	a := &App{connection: "Analytics", explorerWidth: 400, hideSearch: true, explorerCatalog: database.ExplorerCatalog{Engine: database.BigQuery}}
	a.applyCatalog([]database.Schema{
		{Project: table.Project, Name: table.Schema, Loaded: true, Tables: []database.Table{table, view}},
		{Project: table.Project, Name: "unloaded"},
	}, true)
	v := ui.NewTester(func(c *ui.Context) { a.sidebar(c, paletteOf(c)) }, 400, 900)
	project, ok := v.Find("Select analytics-project")
	if !ok {
		t.Fatal("missing project node")
	}
	dataset, ok := v.Find("Select warehouse")
	if !ok || dataset.Y <= project.Y {
		t.Fatal("dataset must appear beneath project")
	}
	for _, object := range []database.Table{table, view} {
		if _, ok := v.Find("Inspect table " + object.Label()); !ok {
			t.Fatalf("missing dataset object %s", object.Name)
		}
	}
	if _, ok := v.Find("Select Tables"); ok {
		t.Fatal("BigQuery tables should be direct dataset children")
	}
	if err := v.Click("Select unloaded"); err != nil {
		t.Fatal(err)
	}
	if !a.treeClosed["schema:"+a.schemas[1].ID()] || a.schemas[1].Loaded || a.busy {
		t.Fatal("selecting a dataset must not expand or load it")
	}
	if err := v.Click("Select analytics-project"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.objectPath, []string{"Database", "Analytics", "analytics-project"}) {
		t.Fatalf("unexpected project breadcrumb: %v", a.objectPath)
	}
	if got := a.tableObjectPath(table); !slices.Equal(got, []string{"Database", "Analytics", "analytics-project", "warehouse", "events"}) {
		t.Fatalf("unexpected table breadcrumb: %v", got)
	}
	if err := v.Click("Toggle analytics-project"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Select warehouse"); ok {
		t.Fatal("collapsing the project must hide its datasets")
	}
	if err := v.Click("Toggle analytics-project"); err != nil {
		t.Fatal(err)
	}
	a.filter = "events"
	v.Frame()
	if _, ok := v.Find("Inspect table " + table.Label()); !ok {
		t.Fatal("table search lost matching project children")
	}
	if a.schemas[1].Loaded || a.busy {
		t.Fatal("search must not fetch unloaded datasets")
	}
}
