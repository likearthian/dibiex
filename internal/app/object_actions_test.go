package app

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestSidebarConnectionAndObjectMenus(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	schemas, err := client.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{client: client, connection: "Demo workspace", query: "SELECT * FROM projects;", explorerWidth: 250, editorPercent: 55}
	a.applyCatalog(schemas, true)
	v := ui.NewTester(a.view, 1180, 780)
	if _, ok := v.Find("Connect…"); ok {
		t.Fatal("title bar connection button still present")
	}
	plus, ok := v.Find("Add connection")
	if !ok || plus.Y < 44 || plus.X+plus.W > 250 {
		t.Fatalf("connection plus must be inside sidebar: %+v", plus)
	}
	if err = v.Click("Add connection"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection {
		t.Fatal("sidebar plus did not open connection form")
	}
	if err = v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}

	openMenu := func() {
		t.Helper()
		r, ok := v.Find("Inspect table projects")
		if !ok {
			t.Fatal("missing project row")
		}
		v.Move(r.X+8, r.Y+r.H/2)
		if err := v.Click("Actions for projects"); err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"Add column…", "Modify / rename…", "Delete…", "Refresh"} {
			if !v.HasText(label) {
				t.Fatalf("missing object action %s", label)
			}
		}
	}
	openMenu()
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "object-menu.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Moving from the row into the popup must retain the menu and its action.
	r, _ := v.Find("Delete…")
	v.Move(r.X+r.W/2, r.Y+r.H/2)
	if err = v.Click("Delete…"); err != nil {
		t.Fatal(err)
	}
	if sql := a.currentEditor().state.Buffer.String(); sql != `DROP TABLE "main"."projects";` {
		t.Fatalf("wrong delete draft: %q", sql)
	}
	if !a.treeClosed["table:"+schemas[0].Tables[0].ID()] || a.activeTable != "" {
		t.Fatal("menu click also selected or expanded the table")
	}
	if _, err = client.Query(ctx, "SELECT * FROM projects"); err != nil {
		t.Fatal("choosing Delete must leave execution to the SQL editor")
	}

	openMenu()
	if err = v.Click("Add column…"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Execute(ctx, a.currentEditor().state.Buffer.String()); err != nil {
		t.Fatal(err)
	}
	m, err := client.TableMetadata(ctx, schemas[0].Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	if m.Columns[len(m.Columns)-1].Name != "new_column" {
		t.Fatal("add-column SQL did not add a column")
	}
	openMenu()
	if err = v.Click("Modify / rename…"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Execute(ctx, a.currentEditor().state.Buffer.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Query(ctx, "SELECT * FROM new_name"); err != nil {
		t.Fatal("rename SQL did not rename the table")
	}

	if err = v.Click("Actions for main"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Add table…"); err != nil {
		t.Fatal(err)
	}
	if !a.showCreateTable || a.sqliteDesigner == nil || !v.HasText("Create table") {
		t.Fatal("SQLite Add table must open the native designer")
	}
}
