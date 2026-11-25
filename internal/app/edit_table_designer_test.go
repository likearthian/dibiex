package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func editDesignerFixture() *tableDesigner {
	snapshot := database.PostgreSQLTableDesign{ObjectID: 42, Table: database.Table{Schema: "public", Name: "items", Kind: "Tables"}, DatabaseTablespace: "pg_default", Definition: database.PostgreSQLTable{Name: "items", Schema: "public", Owner: "app_owner", PartitionMethod: "RANGE", Autovacuum: "Default", Columns: []database.PostgreSQLColumn{{Number: 1, Name: "id", Type: "integer", NotNull: true, PrimaryKey: true, Identity: "ALWAYS"}, {Number: 2, Name: "note", Type: "text", Identity: "None", Comment: "Notes"}}, Constraints: []database.PostgreSQLConstraint{{ID: 12, Name: "items_pkey", Kind: "PRIMARY KEY", Columns: "id"}}, Indexes: []database.PostgreSQLIndex{{ID: 13, Name: "items_pkey", Columns: "id", Method: "btree", ConstraintOwned: true, ReadOnlyReason: "Managed by its table constraint"}}}, Columns: []database.PostgreSQLColumnEdit{{Number: 1, TableOID: 42, Name: "id", Type: "integer", NotNull: true, Original: database.Column{Name: "id", Type: "integer", Nullable: "NO", Identity: "a", PrimaryKey: true}}, {Number: 2, TableOID: 42, Name: "note", Type: "text", Comment: "Notes", Original: database.Column{Name: "note", Type: "text", Nullable: "YES", Description: "Notes"}}}}
	f := &tableDesigner{edit: &snapshot, loaded: true, tab: "General", owners: []string{"app_owner"}, schemas: []string{"public", "audit"}, tablespaces: []string{"Database default", "pg_default"}}
	f.resetEdit()
	return f
}

func TestSharedEditDesignerTabsAndDraft(t *testing.T) {
	f := editDesignerFixture()
	a := &App{showCreateTable: true, tableDesigner: f, connection: "Development", explorerWidth: 260, editorPercent: 60}
	v := ui.NewTester(a.view, 1180, 780)
	if _, ok := v.Find("Edit PostgreSQL table"); !ok {
		t.Fatal("edit did not use the shared table designer")
	}
	for _, tab := range []string{"General", "Columns", "Advanced", "Constraints", "Indexes", "Partitions", "Parameters", "Security", "SQL"} {
		if _, ok := v.Find("Table designer " + tab); !ok {
			t.Fatalf("missing shared tab %s", tab)
		}
	}
	if statements, _, err := f.statements(); err != nil || len(statements) != 0 {
		t.Fatalf("loaded definition is not a no-op: %v %v", statements, err)
	}
	if err := v.Click("Table name"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("renamed")
	if err := v.Click("Table designer Columns"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Column 2 comment"); !ok {
		t.Fatal("loaded columns are not inline in the shared editor")
	}
	if err := v.Click("Column 2 name"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("message")
	if err := v.Click("Column 2 type"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("varchar(80)")
	if f.edit.Definition.Columns[1].Name != "note" || f.edit.Definition.Name != "items" {
		t.Fatal("editing mutated the original definition")
	}
	if err := v.Click("Table designer Constraints"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table constraint"); err != nil {
		t.Fatal(err)
	}
	f.definition.Constraints[1].Kind, f.definition.Constraints[1].Name, f.definition.Constraints[1].Expression = "CHECK", "message_length", "length(message) <= 80"
	if err := v.Click("Table designer Indexes"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Add table index"); err != nil {
		t.Fatal(err)
	}
	f.definition.Indexes[1].Name, f.definition.Indexes[1].Columns = "message_idx", "message"
	if err := v.Click("Table designer Parameters"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Table fillfactor"); err != nil {
		t.Fatal(err)
	}
	v.Type("85")
	if err := v.Click("Table designer Partitions"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Partitioned table"); err != nil {
		t.Fatal(err)
	}
	if f.definition.Partitioned {
		t.Fatal("existing partition layout became editable")
	}
	if err := v.Click("Table designer SQL"); err != nil {
		t.Fatal(err)
	}
	statements, destructive, err := f.statements()
	preview := strings.Join(statements, ";\n")
	if err != nil || destructive || strings.Contains(preview, "CREATE TABLE") {
		t.Fatalf("incorrect ALTER preview: %s %v", preview, err)
	}
	for _, want := range []string{`RENAME COLUMN "note" TO "message"`, `TYPE varchar(80)`, `ADD CONSTRAINT "message_length"`, `CREATE INDEX "message_idx"`, `SET (fillfactor = 85)`, `RENAME TO "renamed"`} {
		if !strings.Contains(preview, want) {
			t.Fatalf("missing %q: %s", want, preview)
		}
	}
	if _, ok := v.Find("Copy Alter table SQL preview"); !ok {
		t.Fatal("edit SQL preview is not labeled")
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		v.SetDark(true)
		if err := v.Click("Table designer Columns"); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, "edit-shared-designer.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, v.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.errorText = "Permission denied"
	v.Frame()
	if !v.HasText("Permission denied") || f.definition.Columns[1].Name != "message" {
		t.Fatal("save error discarded the draft")
	}
	if err := v.Click("Reset table designer"); err != nil {
		t.Fatal(err)
	}
	if f.definition.Name != "items" || f.definition.Columns[1].Name != "note" || len(f.definition.Constraints) != 1 || len(f.definition.Indexes) != 1 {
		t.Fatal("Reset did not restore the original live definition")
	}
	for _, size := range [][2]int{{1180, 780}, {800, 560}} {
		v.SetSize(size[0], size[1])
		button, ok := v.Find("Save table changes")
		if !ok || button.X < 0 || button.X+button.W > float32(size[0])+1 || button.Y+button.H > float32(size[1])+1 {
			t.Fatalf("edit Save clipped at %v: %+v", size, button)
		}
	}
}

func TestSharedEditDesignerConfirmsRemovals(t *testing.T) {
	f := editDesignerFixture()
	f.tab = "Columns"
	a := &App{tableDesigner: f, showCreateTable: true}
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Remove column 2"); err != nil {
		t.Fatal(err)
	}
	_, destructive, err := f.statements()
	if err != nil || !destructive {
		t.Fatalf("missing drop classification: %v", err)
	}
	if _, ok := v.Find("Confirm table designer removals"); !ok {
		t.Fatal("drop confirmation missing")
	}
	if f.confirmDrops {
		t.Fatal("removals were pre-approved")
	}
	if err := v.Click("Confirm table designer removals"); err != nil {
		t.Fatal(err)
	}
	if !f.confirmDrops {
		t.Fatal("confirmation did not update")
	}
}
