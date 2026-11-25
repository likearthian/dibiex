package app

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestColumnSelectionAndBreadcrumb(t *testing.T) {
	table := database.Table{Schema: "public", Name: "address", Kind: "Tables"}
	columns := []database.Column{{Name: "address_id", Type: "integer", PrimaryKey: true, Nullable: "NO", Ordinal: 1, Default: "nextval('address_address_id_seq'::regclass)"}, {Name: "address", Type: "varchar(50)", Nullable: "NO", Ordinal: 2, Description: "Street address", Properties: []database.Property{{Name: "attname", Value: "address"}}}, {Name: "address2", Type: "varchar(50)", Nullable: "YES", Ordinal: 3}, {Name: "payload", Type: "RECORD", Fields: []database.Column{{Name: "street.name", Type: "STRING", Nullable: "YES"}}}}
	a := &App{connection: "Demo PostgreSQL", explorerWidth: 440, editorPercent: 60, hideSearch: true, hideResults: true, showFiles: true, explorerCatalog: database.ExplorerCatalog{Engine: database.PostgreSQL, Database: "datagrip", FetchedAt: time.Now()}}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{table}}}, true)
	a.treeClosed["group:"+a.schemas[0].ID()+":Tables"] = false
	a.treeClosed["table:"+table.ID()] = false
	a.tableSectionsOpen = map[string]bool{sectionKey(table, "columns"): true}
	a.metadata = map[string]database.TableMetadata{table.ID(): {Columns: columns, Kind: "Tables", FetchedAt: time.Now()}}
	ui.RegisterFont(monoFont, "JetBrains Mono")
	a.tableSectionsOpen[sectionKey(table, "columns")] = false
	a.inspectTable(table)
	v := ui.NewTester(a.view, 1472, 884)
	v.SetDark(true)
	if err := v.Click("Column address"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Back to table"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Inspected table " + table.Label()); !ok || a.inspectorColumn != nil || a.selectedTree != "table:"+table.ID() || !slices.Equal(a.objectPath, a.tableObjectPath(table)) {
		t.Fatal("Back to table did not restore the parent table Inspector and selection")
	}
	if err := v.Click("Hide Inspector"); err != nil {
		t.Fatal(err)
	}
	a.tableSectionsOpen[sectionKey(table, "columns")] = true
	v.Frame()
	if err := v.Click("Column address"); err != nil {
		t.Fatal(err)
	}
	expected := []string{"Database", "Demo PostgreSQL", "datagrip", "public", "tables", "address", "columns", "address"}
	if !a.showInspector || a.inspectorColumn == nil || a.inspectorColumn.Column.Name != "address" {
		t.Fatal("column did not open Inspector")
	}
	if !slices.Equal(a.objectPath, expected) {
		t.Fatalf("wrong breadcrumb: %v", a.objectPath)
	}
	if a.selectedTree != columnTreeKey(table, []string{"address"}) {
		t.Fatal("column selection highlight not retained")
	}
	for _, label := range []string{"Inspected column address", "Data type", "Nullable", "Position", "Description", "Street address", "Object breadcrumb"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing column Inspector detail %s", label)
		}
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "column-inspector-breadcrumb.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Click("Column address_id"); err != nil {
		t.Fatal(err)
	}
	if !a.inspectorColumn.Column.PrimaryKey || !v.HasText("nextval('address_address_id_seq'::regclass)") {
		t.Fatal("Inspector kept previous column metadata")
	}
	if err := v.Click("Toggle field payload"); err != nil {
		t.Fatal(err)
	}
	if a.inspectorColumn.Column.Name != "address_id" {
		t.Fatal("field expansion changed column selection")
	}
	if err := v.Click("Column payload.street.name"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.inspectorColumn.Path, []string{"payload", "street.name"}) {
		t.Fatalf("nested field identity lost: %v", a.inspectorColumn.Path)
	}
	expected = append(expected[:6], "columns", "payload", "street.name")
	if !slices.Equal(a.objectPath, expected) {
		t.Fatalf("nested breadcrumb: %v", a.objectPath)
	}
	m := a.metadata[table.ID()]
	m.Columns = append([]database.Column(nil), columns...)
	m.Columns[3].Fields = []database.Column{{Name: "street.name", Type: "JSON", Nullable: "NO"}}
	a.metadata[table.ID()] = m
	a.refreshColumnInspection(table, m)
	v.Frame()
	if a.inspectorColumn.Column.Type != "JSON" {
		t.Fatal("metadata refresh did not update selected nested field")
	}
	if err := v.Click("Hide Inspector"); err != nil {
		t.Fatal(err)
	}
	v.SetSize(800, 560)
	crumb, ok := v.Find("Object breadcrumb")
	if !ok || crumb.X < 0 || crumb.X+crumb.W > 801 {
		t.Fatalf("breadcrumb outside narrow window: %+v", crumb)
	}
	if a.breadcrumbScroll.MaxX > 0 && a.breadcrumbScroll.X < a.breadcrumbScroll.MaxX-1 {
		t.Fatal("selected object is not visible at end of breadcrumb")
	}
	m.Columns[3].Fields = nil
	a.refreshColumnInspection(table, m)
	v.Frame()
	if a.inspectorColumn != nil || len(a.objectPath) != 6 {
		t.Fatal("removed column retained stale selection/path")
	}
}
