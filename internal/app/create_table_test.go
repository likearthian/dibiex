package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestColumnTypeDropdownRemainsEditable(t *testing.T) {
	f := &tableDesigner{definition: database.PostgreSQLTable{Name: "contacts", Schema: "public"}, tab: "Columns", owner: "Current role", tablespace: "Database default"}
	f.addColumn()
	f.definition.Columns[0].Name = "value"
	a := &App{connection: "Development", explorerWidth: 260, editorPercent: 60, showCreateTable: true, tableDesigner: f}
	v := ui.NewTester(a.view, 1180, 780)
	for _, step := range []struct{ choice, edited string }{{"varchar(255)", "varchar(80)"}, {"numeric(18,2)", "numeric(12,4)"}} {
		if err := v.Click("Choose column 1 data type"); err != nil {
			t.Fatal(err)
		}
		if err := v.Click(step.choice); err != nil {
			t.Fatal(err)
		}
		if f.definition.Columns[0].Type != step.choice {
			t.Fatal("dropdown did not apply selected type")
		}
		if err := v.Click("Column 1 type"); err != nil {
			t.Fatal(err)
		}
		v.Key(ui.Cmd, ui.KeyA)
		v.Type(step.edited)
		if err := v.Click("Column 1 name"); err != nil {
			t.Fatal(err)
		}
		statements, err := f.table().Statements()
		if err != nil || !strings.Contains(statements[0], step.edited) {
			t.Fatalf("edited type was lost: %v, %v", statements, err)
		}
	}
	if err := v.Click("Column 1 type"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("public.email_type")
	if err := v.Click("Column 1 name"); err != nil {
		t.Fatal(err)
	}
	if f.definition.Columns[0].Type != "public.email_type" {
		t.Fatal("custom type reverted after losing focus")
	}
}

func TestTableDesignerLayoutAndDDL(t *testing.T) {
	f := &tableDesigner{definition: database.PostgreSQLTable{Schema: "public", PartitionMethod: "RANGE", Autovacuum: "Default"}, tab: "General", owner: "Current role", tablespace: "Database default", owners: []string{"Current role", "app_owner"}, tablespaces: []string{"Database default", "pg_default"}, schemas: []string{"public", "audit"}}
	a := &App{connection: "Development", explorerWidth: 260, editorPercent: 60, showCreateTable: true, tableDesigner: f, themeMode: "dark"}
	v := ui.NewTester(a.view, 1180, 780)
	for _, label := range []string{"Create PostgreSQL table", "Table name", "Table owner", "Table schema", "Table tablespace", "Partitioned table", "Table comment", "Close create table", "Reset table designer", "Save new table"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing designer control %s", label)
		}
	}
	if !v.HasText("Table name is required") {
		t.Fatal("required field validation missing")
	}
	f.definition.Name = "invoices"
	if err := v.Click("Table designer Columns"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table column"); err != nil {
		t.Fatal(err)
	}
	f.definition.Columns[0].Name = "id"
	f.definition.Columns[0].Type = "bigint"
	f.definition.Columns[0].Identity = "ALWAYS"
	f.definition.Columns[0].PrimaryKey = true
	if err := v.Click("Add table column"); err != nil {
		t.Fatal(err)
	}
	f.definition.Columns[1].Name = "amount"
	f.definition.Columns[1].Type = "numeric(18,2)"
	f.definition.Columns[1].Default = "0"
	if err := v.Click("Table designer Constraints"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table constraint"); err != nil {
		t.Fatal(err)
	}
	f.definition.Constraints[0].Kind = "CHECK"
	f.definition.Constraints[0].Expression = "amount >= 0"
	if err := v.Click("Table designer SQL"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Copy Create table SQL"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`CREATE TABLE "public"."invoices"`, `GENERATED ALWAYS AS IDENTITY`, `CHECK (amount >= 0)`, `PRIMARY KEY ("id")`, "BEGIN;", "COMMIT;"} {
		if !strings.Contains(v.Clipboard(), sql) {
			t.Fatalf("SQL preview missing %s", sql)
		}
	}
	for _, tab := range []string{"Advanced", "Partitions", "Parameters", "Security", "General"} {
		if err := v.Click("Table designer " + tab); err != nil {
			t.Fatal(err)
		}
	}
	if output := os.Getenv("DBCRAFT_SCREENSHOTS"); output != "" {
		file, err := os.Create(filepath.Join(output, "create-table-general.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, v.Image()); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	for _, size := range [][2]int{{800, 560}, {1472, 884}} {
		v.SetSize(size[0], size[1])
		for _, label := range []string{"Create PostgreSQL table", "Close create table", "Save new table"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
				t.Fatalf("designer clipped at %v: %s %+v", size, label, r)
			}
		}
	}
	if err := v.Click("Table designer Columns"); err != nil {
		t.Fatal(err)
	}
	firstID := f.columnIDs[0]
	secondID := f.columnIDs[1]
	if err := v.Click("Remove column 1"); err != nil {
		t.Fatal(err)
	}
	if len(f.definition.Columns) != 1 || f.definition.Columns[0].Name != "amount" || f.columnIDs[0] != secondID || f.columnIDs[0] == firstID {
		t.Fatal("column removal lost the surviving row's identity")
	}
	if err := v.Click("Close create table"); err != nil {
		t.Fatal(err)
	}
	if a.showCreateTable {
		t.Fatal("close did not dismiss designer")
	}
}
