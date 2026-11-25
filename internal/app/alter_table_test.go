package app

import (
	"context"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestNativeColumnEditorDraftAndPreview(t *testing.T) {
	table := database.Table{Schema: "public", Name: "items", Kind: "Tables"}
	f := &tableActionForm{table: table, kind: "Edit column", columnName: "note", loaded: true, columnEdit: database.PostgreSQLColumnEdit{Original: database.Column{Name: "note", Type: "text", Nullable: "YES"}, Name: "note", Type: "text"}}
	a := &App{tableAction: f, showTableAction: true, explorerWidth: 260, editorPercent: 60}
	v := ui.NewTester(a.view, 1180, 780)
	for _, label := range []string{"Column editor Column name", "Column editor Data type", "Column editor USING SQL expression (optional)", "Column editor Default SQL expression (empty removes default)", "Not null", "Column editor Comment", "Execute table action"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing %s", label)
		}
	}
	if err := v.Click("Column editor Column name"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("message")
	if err := v.Click("Column editor Data type"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("varchar(80)")
	preview, err := f.editSQL()
	if err != nil || !strings.Contains(preview, `RENAME COLUMN "note" TO "message"`) || !strings.Contains(preview, "TYPE varchar(80)") {
		t.Fatalf("draft or SQL preview lost: %s %v", preview, err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		v.SetDark(true)
		file, err := os.Create(filepath.Join(dir, "edit-column.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, v.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.errorText = "Conversion failed"
	v.Frame()
	if !v.HasText("Conversion failed") || f.columnEdit.Name != "message" || f.columnEdit.Type != "varchar(80)" {
		t.Fatal("error discarded the column draft")
	}
	for _, size := range [][2]int{{1180, 780}, {800, 560}} {
		v.SetSize(size[0], size[1])
		button, ok := v.Find("Execute table action")
		if !ok || button.X < 0 || button.X+button.W > float32(size[0])+1 || button.Y+button.H > float32(size[1])+1 {
			t.Fatalf("save clipped at %v: %+v", size, button)
		}
	}
}

func TestNativeTableEditorAndBackPreservesDraft(t *testing.T) {
	table := database.Table{Schema: "public", Name: "items"}
	parent := &tableActionForm{table: table, kind: "Properties", loaded: true, action: database.TableAction{Kind: "Properties", Name: "renamed_draft", ReferenceSchema: "public"}, metadata: database.TableMetadata{Columns: []database.Column{{Name: "note", Type: "text"}}}}
	a := &App{showTableAction: true, tableAction: parent, explorerWidth: 260, editorPercent: 60}
	v := ui.NewTester(a.view, 1180, 780)
	for _, label := range []string{"Table name", "Schema", "Owner", "Comment", "Edit table Columns"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing table editor field %s", label)
		}
	}
	if err := v.Click("Edit table Columns"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Edit column note"); !ok {
		t.Fatal("table editor has no column action")
	}
	child := &tableActionForm{table: table, kind: "Edit column", loaded: true, parent: parent, columnEdit: database.PostgreSQLColumnEdit{Original: database.Column{Name: "note", Type: "text", Nullable: "YES"}, Name: "note", Type: "text"}}
	a.tableAction = child
	v.Frame()
	if err := v.Click("Close table action"); err != nil {
		t.Fatal(err)
	}
	if a.tableAction != parent || !a.showTableAction || parent.action.Name != "renamed_draft" || parent.propertyTab != "Columns" {
		t.Fatal("returning from column editor lost table draft")
	}
	if !editablePostgreSQLTable(database.PostgreSQL, table) || editablePostgreSQLTable(database.SQLite, table) || editablePostgreSQLTable(database.PostgreSQL, database.Table{Kind: "Views"}) {
		t.Fatal("incorrect engine/object editing scope")
	}
}

func TestEditedColumnSelectionKeepsOwner(t *testing.T) {
	before := database.Table{Schema: "public", Name: "items"}
	a := &App{}
	a.inspectColumn(before, []string{"note"}, database.Column{Name: "note"})
	after := database.Table{Schema: "audit", Name: "renamed"}
	a.editedTableSelection(before, after, "note", "message")
	if a.inspectorColumn.Table.ID() != after.ID() || a.inspectorColumn.Path[0] != "message" || a.activeTable != after.ID() || a.selectedTree != columnTreeKey(after, []string{"message"}) {
		t.Fatal("edited object selection did not follow its table/column")
	}
	other := &App{}
	other.inspectColumn(before, []string{"note"}, database.Column{Name: "note"})
	a.editedTableSelection(before, after, "note", "message")
	if other.inspectorColumn.Table.ID() != before.ID() || other.inspectorColumn.Column.Name != "note" {
		t.Fatal("edit affected another workspace")
	}
}

func TestPostgreSQLColumnEditMenuIntegration(t *testing.T) {
	dsn := os.Getenv("DBCRAFT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_TEST_POSTGRES_DSN to test native PostgreSQL edit menus")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test DSN")
	}
	password, _ := u.User.Password()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := database.Connect(ctx, database.Config{Engine: database.PostgreSQL, Host: u.Hostname(), Port: u.Port(), Database: strings.TrimPrefix(u.Path, "/"), User: u.User.Username(), Password: password, SSLMode: u.Query().Get("sslmode")})
	if err != nil {
		t.Fatal("could not connect to PostgreSQL test server")
	}
	defer client.Close()
	table := database.Table{Schema: "public", Name: "items", Kind: "Tables"}
	a := &App{client: client}
	v := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.columnTree(c, paletteOf(c), []database.Column{{Name: "note", Type: "text"}}, "test", "", 0, 0, table)
		})
	}, 600, 400)
	if err := v.Click("Actions for column public.items.note"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Edit column…"); err != nil {
		t.Fatal(err)
	}
	if !a.showTableAction || a.tableAction == nil || a.tableAction.kind != "Edit column" || a.tableAction.columnName != "note" || len(a.tabs) != 0 {
		t.Fatal("column menu did not open its native editor")
	}
	found := false
	for _, item := range a.postgresTableItems(table) {
		if item.label == "Edit table…" {
			item.action()
			found = a.showCreateTable && a.tableDesigner != nil && a.tableDesigner.edit != nil && !a.showTableAction
		}
	}
	if !found {
		t.Fatal("table menu did not open its native editor")
	}
}
