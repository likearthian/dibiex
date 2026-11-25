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

func TestSequenceLeafInspector(t *testing.T) {
	first := database.Definition{Name: "actor_actor_id_seq", Kind: "sequences", Properties: []database.Property{{Name: "data_type", Value: "bigint"}, {Name: "owner", Value: "postgres"}, {Name: "sequence", Children: []database.Property{{Name: "seqstart", Value: "1"}, {Name: "seqincrement", Value: "1"}, {Name: "seqmin", Value: "1"}, {Name: "seqmax", Value: "9223372036854775807"}, {Name: "seqcache", Value: "32"}, {Name: "seqcycle", Value: "false"}}}}}
	second := database.Definition{Name: "address_address_id_seq", Kind: "sequences", Properties: []database.Property{{Name: "data_type", Value: "integer"}, {Name: "owner", Value: "warehouse"}}}
	a := &App{connection: "Application PostgreSQL", explorerWidth: 400, editorPercent: 60, hideSearch: true, hideResults: true, showFiles: true, explorerCatalog: database.ExplorerCatalog{Version: 1, Engine: database.PostgreSQL, Database: "app", FetchedAt: time.Now(), Groups: []database.ExplorerGroup{{Scope: "schema:public", Name: "sequences", State: "loaded", Items: []database.Definition{first, second}}}}}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true}}, true)
	a.treeClosed["extra:schema:public:sequences"] = false
	v := ui.NewTester(a.view, 1472, 884)
	v.SetDark(true)
	before, _ := v.Find("Inspect sequences address_address_id_seq")
	if err := v.Click("Inspect sequences actor_actor_id_seq"); err != nil {
		t.Fatal(err)
	}
	if !a.showInspector || a.inspectorObject == nil || a.inspectorObject.Object.Name != first.Name {
		t.Fatal("sequence selection did not open Inspector")
	}
	after, _ := v.Find("Inspect sequences address_address_id_seq")
	if before.Y != after.Y || a.propertyOpen["extra:schema:public:sequences:"+first.Name] {
		t.Fatal("sequence expanded in tree")
	}
	for _, label := range []string{"Sequence type for actor_actor_id_seq", "Inspected sequence actor_actor_id_seq", "Data type", "Owner", "Start value", "Increment", "Minimum", "Maximum", "Cache", "Cycle", "Native metadata"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing sequence detail %s", label)
		}
	}
	if err := v.Click("Inspect sequences address_address_id_seq"); err != nil {
		t.Fatal(err)
	}
	if sequenceDataType(a.inspectorObject.Object) != "integer" || !v.HasText("warehouse") {
		t.Fatal("Inspector retained previous sequence details")
	}
	if err := v.Click("Inspect sequences actor_actor_id_seq"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "sequence-inspector.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	a.explorerCatalog.Groups[0].Items[0].Properties[1].Value = "new_owner"
	a.refreshCatalogInspection()
	v.Frame()
	if !v.HasText("new_owner") {
		t.Fatal("refreshed sequence not applied to Inspector")
	}
	a.explorerCatalog.Groups[0].Items = nil
	a.refreshCatalogInspection()
	v.Frame()
	if a.inspectorObject != nil {
		t.Fatal("removed sequence retained in Inspector")
	}
}
