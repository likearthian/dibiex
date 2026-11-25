package app

import (
	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceNavigation(t *testing.T) {
	a := &App{connection: "Demo workspace", status: "Ready", query: "SELECT * FROM projects;", schemas: []database.Schema{{Name: "main", Loaded: true, Tables: []database.Table{{Schema: "main", Name: "projects"}, {Schema: "main", Name: "tasks"}}}}, selected: -1, explorerWidth: 250, editorPercent: 55,
		result: database.Result{Columns: []string{"name", "value"}, Rows: [][]string{{"Example", "NULL"}, {"Attachment", "[BLOB: 4 bytes]"}}}}
	ui.RegisterFont(monoFont, "JetBrains Mono")
	a.addQuery(a.query)
	v := ui.NewTester(a.view, 1180, 780)
	if !v.HasText("projects") || !v.HasText("NULL") {
		t.Fatal("database objects and result values must be visible")
	}
	for _, label := range []string{"Hide Database Explorer · Alt+1", "Show Database Explorer · Alt+1", "Output", "Result 1", "Hide results · Alt+8", "Toggle Services · Alt+8"} {
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	a.filter = "task"
	v.Frame()
	if v.HasText("projects") || !v.HasText("tasks") {
		t.Fatal("search must filter database objects")
	}
	a.filter = ""
	v.Frame()
	for _, size := range [][2]int{{1180, 780}, {800, 560}} {
		v.SetSize(size[0], size[1])
		for _, label := range []string{"console.sql", "Export Data", "Search tables and views"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
				t.Fatalf("%q is outside %v: %+v", label, size, r)
			}
		}
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		v.SetSize(1180, 780)
		for _, dark := range []bool{false, true} {
			v.SetDark(dark)
			name := "workspace-light.png"
			if dark {
				name = "workspace-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, name))
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
}

func TestConnectionDialog(t *testing.T) {
	a := &App{connection: "Demo workspace", explorerWidth: 250, editorPercent: 55}
	a.open()
	a.addQuery(a.query)
	v := ui.NewTester(a.view, 800, 560)
	for _, engine := range []database.Engine{database.PostgreSQL, database.MySQL, database.BigQuery, database.SQLite} {
		a.showConnection = true
		a.connectionForm.engine = string(engine)
		v.Frame()
		for _, label := range []string{"Connect", "Cancel", "Database engine"} {
			r, ok := v.Find(label)
			if !ok || r.Y < 0 || r.Y+r.H > 560 {
				t.Fatalf("%s: %s clipped: %+v", engine, label, r)
			}
		}
		if engine == database.SQLite {
			if _, ok := v.Find("New file…"); !ok {
				t.Fatal("SQLite form has no new database action")
			}
		}
		if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, string(engine)+"-connection.png"))
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
	a.connectionForm.engine = string(database.PostgreSQL)
	a.connectionForm.config.Host = ""
	a.connectionForm.config.Password = "test-password"
	v.Frame()
	if err := v.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection || a.connectionForm.errorText == "" {
		t.Fatal("invalid settings must keep connection form open with a message")
	}
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.showConnection || a.connectionForm.config.Password != "" {
		t.Fatal("cancel must dismiss form and clear password")
	}
}

func TestExplorerHierarchy(t *testing.T) {
	a := &App{connection: "warehouse", explorerWidth: 285, editorPercent: 55}
	a.applyCatalog([]database.Schema{
		{Name: "analytics", Loaded: true, Tables: []database.Table{{Schema: "analytics", Name: "daily_orders", Kind: "Views"}}},
		{Name: "public", Loaded: true, Tables: []database.Table{{Schema: "public", Name: "orders", Kind: "Tables"}}},
		{Name: "empty", Loaded: true},
	}, true)
	a.addQuery(a.query)
	v := ui.NewTester(a.view, 1180, 780)
	hasText := func(want string) bool {
		for _, text := range v.Texts() {
			if text == want {
				return true
			}
		}
		return false
	}

	for _, label := range []string{"analytics", "public", "Tables", "Views", "daily_orders", "orders", "empty"} {
		if !hasText(label) {
			t.Fatalf("missing tree node %s", label)
		}
	}
	if err := v.Click("Toggle public"); err != nil {
		t.Fatal(err)
	}
	if hasText("orders") || !hasText("daily_orders") {
		t.Fatal("collapsing one schema must leave other schemas open")
	}
	a.filter = "orders"
	v.Frame()
	if !hasText("orders") || !hasText("daily_orders") {
		t.Fatal("search must reveal matching objects through collapsed ancestors")
	}
	a.filter = ""
	v.Frame()
	if hasText("orders") {
		t.Fatal("clearing search must preserve the schema's collapse state")
	}
	if err := v.Click("Toggle public"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Toggle Views"); err != nil {
		t.Fatal(err)
	}
	if hasText("daily_orders") || !hasText("orders") {
		t.Fatal("object groups must collapse independently")
	}
	if err := v.Click("Toggle warehouse"); err != nil {
		t.Fatal(err)
	}
	if hasText("analytics") || hasText("public") {
		t.Fatal("connection collapse must hide descendants")
	}
	if err := v.Click("Toggle warehouse"); err != nil {
		t.Fatal(err)
	}
	a.treeClosed["group:"+a.schemas[0].ID()+":Views"] = false
	v.Frame()
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "database-tree.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	a.applyCatalog([]database.Schema{{Project: "warehouse", Name: "raw"}, {Project: "warehouse", Name: "reporting"}}, true)
	v.Frame()
	if !a.treeClosed["schema:"+a.schemas[0].ID()] || !a.treeClosed["schema:"+a.schemas[1].ID()] {
		t.Fatal("unloaded datasets must start collapsed")
	}
	if hasText("Loading…") {
		t.Fatal("rendering a project must not begin dataset loads")
	}
}
