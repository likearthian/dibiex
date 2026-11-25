package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestReferenceWorkspace(t *testing.T) {
	a := &App{connection: "Local database", status: "Ready", selected: -1, explorerWidth: 260, editorPercent: 60, hideResults: true, showFiles: true, hideSearch: true}
	a.applyCatalog([]database.Schema{{Name: "main", Loaded: true, Tables: []database.Table{{Schema: "main", Name: "accounts", Kind: "Tables"}, {Schema: "main", Name: "events", Kind: "Tables"}, {Schema: "main", Name: "orders", Kind: "Tables"}, {Schema: "main", Name: "active_accounts", Kind: "Views"}}}}, true)
	a.selectedTree = "group:" + a.schemas[0].ID() + ":Tables"
	ui.RegisterFont(monoFont, "JetBrains Mono")
	a.addQuery(a.query)
	v := ui.NewTester(a.view, 1472, 884)
	v.SetDark(true)
	for _, label := range []string{"Database Explorer", "Files", "Scratches and Consoles", "Execute", "Explain", "Stop", "New query tab"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing reference control %s", label)
		}
	}
	if err := v.Click("New query tab"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Open console console.sql"); err != nil {
		t.Fatal(err)
	}
	if a.activeTab != 0 {
		t.Fatal("Files did not switch query tabs")
	}
	if err := v.Click("Inspector"); err != nil {
		t.Fatal(err)
	}
	if !a.showInspector {
		t.Fatal("Inspector did not open")
	}
	if err := v.Click("Toggle Files"); err != nil {
		t.Fatal(err)
	}
	if !a.showFiles || a.showInspector {
		t.Fatal("Files did not replace Inspector")
	}
	before := a.rightWidth
	files, _ := v.Find("Right sidebar content")
	divider := files.X - 4.5
	v.Press(divider, 300)
	v.Move(divider-80, 300)
	v.Release(divider-80, 300)
	v.Frame()
	if a.rightWidth < before+60 {
		t.Fatalf("right sidebar did not resize: before %.1f after %.1f", before, a.rightWidth)
	}
	resized := a.rightWidth
	v.SetSize(1600, 884)
	if a.rightWidth < resized-1 || a.rightWidth > resized+1 {
		t.Fatal("window resize changed preferred sidebar width")
	}
	if err := v.Click("Inspector"); err != nil {
		t.Fatal(err)
	}
	if a.rightWidth < resized-1 || a.rightWidth > resized+1 {
		t.Fatal("Inspector lost resized sidebar width")
	}
	if err := v.Click("Toggle Files"); err != nil {
		t.Fatal(err)
	}
	v.SetSize(1472, 884)
	if err := v.Click("Toggle Scratches and Consoles"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Open console console.sql"); ok {
		t.Fatal("Files group did not collapse")
	}
	if err := v.Click("Toggle Scratches and Consoles"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "reference-dark.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	v.SetSize(800, 560)
	if err := v.Click("Search database objects"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Files"); ok {
		t.Fatal("Files should collapse on a narrow window")
	}
	for _, label := range []string{"New query tab", "Execute", "Search tables and views"} {
		r, ok := v.Find(label)
		if !ok || r.X < 0 || r.X+r.W > 801 {
			t.Fatalf("narrow window control clipped: %s %+v", label, r)
		}
	}
}
