package app

import (
	"strings"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestDatabaseAndServerObjectsOpenInspector(t *testing.T) {
	a := &App{connection: "Warehouse", explorerWidth: 400, editorPercent: 60, hideSearch: true, hideResults: true, showFiles: true, explorerCatalog: database.ExplorerCatalog{Engine: database.PostgreSQL, Database: "app", FetchedAt: time.Now()}}
	examples := []struct{ scope, group, name string }{{"database", "access methods", "btree"}, {"database", "casts", "integer → bigint"}, {"database", "extensions", "plpgsql"}, {"database", "languages", "plpgsql"}, {"server", "roles", "analyst"}, {"server", "tablespaces", "pg_default"}}
	for _, e := range examples {
		a.explorerCatalog.Groups = append(a.explorerCatalog.Groups, database.ExplorerGroup{Scope: e.scope, Name: e.group, State: "loaded", Items: []database.Definition{{Name: e.name, Kind: e.group, Properties: []database.Property{{Name: "Marker", Value: e.group}}}}})
	}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true}}, true)
	for _, e := range examples {
		a.treeClosed["extra:"+e.scope+":"+e.group] = false
	}
	a.propertyOpen = map[string]bool{"extra:server:roles:analyst": true}
	v := ui.NewTester(a.view, 1472, 1000)
	v.SetDark(true)
	for _, e := range examples {
		before, _ := v.Find("Inspect tablespaces pg_default")
		if err := v.Click("Inspect " + e.group + " " + e.name); err != nil {
			t.Fatal(err)
		}
		if !a.showInspector || a.inspectorObject == nil || a.inspectorObject.Group != e.group || a.inspectorObject.Scope != e.scope {
			t.Fatalf("%s did not open correct Inspector", e.group)
		}
		if _, ok := v.Find("Inspected catalog object " + e.name); !ok {
			t.Fatalf("missing object Inspector for %s", e.name)
		}
		if !v.HasText("Marker: " + e.group) {
			t.Fatalf("Inspector metadata not shown for %s", e.group)
		}
		after, _ := v.Find("Inspect tablespaces pg_default")
		if before.Y != after.Y {
			t.Fatalf("%s expanded inline details", e.group)
		}
		if !strings.HasSuffix(strings.Join(a.objectPath, "/"), e.group+"/"+e.name) {
			t.Fatalf("wrong breadcrumb: %v", a.objectPath)
		}
		if err := v.Click("Inspect " + e.group + " " + e.name); err != nil {
			t.Fatal(err)
		}
		if a.inspectorObject == nil {
			t.Fatal("repeat selection closed Inspector")
		}
	}
	if err := v.Click("Inspect languages plpgsql"); err != nil {
		t.Fatal(err)
	}
	a.explorerCatalog.Groups[3].Items = []database.Definition{{Name: "plpgsql", Kind: "languages", Properties: []database.Property{{Name: "Marker", Value: "updated language"}}}}
	a.refreshCatalogInspection()
	v.Frame()
	if !v.HasText("Marker: updated language") {
		t.Fatal("refresh did not preserve group identity for same-named objects")
	}
	a.explorerCatalog.Groups[3].Items = nil
	a.refreshCatalogInspection()
	v.Frame()
	if a.inspectorObject != nil {
		t.Fatal("removed catalog object retained in Inspector")
	}
	if !strings.HasSuffix(strings.Join(a.objectPath, "/"), "Database Objects/languages") {
		t.Fatalf("removed object breadcrumb did not return to folder: %v", a.objectPath)
	}
}
