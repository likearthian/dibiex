package app

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestSQLiteTableDesignerNativeFlow(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Local SQLite", explorerWidth: 260, editorPercent: 60}
	a.openTableDesigner("main")
	if a.sqliteDesigner == nil || a.tableDesigner != nil {
		t.Fatal("SQLite connection did not choose its own native designer")
	}
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Table name"); err != nil {
		t.Fatal(err)
	}
	v.Type("notes")
	if err := v.Click("Table designer Columns"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table column"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Column 1 name"); err != nil {
		t.Fatal(err)
	}
	v.Type("id")
	if err := v.Click("Choose column 1 data type"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("INTEGER"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Column 1 primary key", "Column 1 autoincrement", "Add table column", "Column 2 name"} {
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	v.Type("label")
	if err := v.Click("Choose column 2 data type"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("VARCHAR(255)"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Column 2 type"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("VARCHAR(80)")
	if err := v.Click("Column 2 name"); err != nil {
		t.Fatal(err)
	}
	f := a.sqliteDesigner
	if f.definition.Columns[1].Type != "VARCHAR(80)" {
		t.Fatal("selected data type did not remain editable")
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		file, err := os.Create(filepath.Join(dir, "create-sqlite-table-columns.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, v.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Click("Table designer Constraints"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table constraint"); err != nil {
		t.Fatal(err)
	}
	f.definition.Constraints[0].Kind = "CHECK"
	f.definition.Constraints[0].Expression = "length(label) <= 80"
	v.Frame()
	if err := v.Click("Table designer SQL"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Copy SQL preview"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`CREATE TABLE "main"."notes"`, `"id" "INTEGER" PRIMARY KEY AUTOINCREMENT`, `"label" "VARCHAR"(80)`, "CHECK (length(label) <= 80)"} {
		if !strings.Contains(v.Clipboard(), want) {
			t.Fatalf("SQLite SQL preview missing %s: %s", want, v.Clipboard())
		}
	}
	if len(a.tabs) != 0 {
		t.Fatal("designer created an unwanted query tab")
	}
	a.showInspector = true
	for _, size := range [][2]int{{800, 560}, {1472, 884}} {
		v.SetSize(size[0], size[1])
		for _, label := range []string{"Create SQLite table", "Save new table", "Close create table"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
				t.Fatalf("SQLite dialog clipped at %v: %s %+v", size, label, r)
			}
		}
	}
	v.SetSize(800, 560)
	if err := v.Click("Close create table"); err != nil || a.showCreateTable {
		t.Fatalf("Inspector drawer blocked the table dialog: %v", err)
	}
}
