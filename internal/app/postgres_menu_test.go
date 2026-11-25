package app

import (
	"dibiex/assets"
	"encoding/xml"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestPostgreSQLTableMenuAndQueryTool(t *testing.T) {
	a := &App{connection: "Development"}
	table := database.Table{Schema: "a schema", Name: `odd"table`}
	v := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			row := ui.Row(c).FillWidth().Height(28)
			row.Children(func() {
				ui.Text(c, table.Name).Grow(1)
				a.postgresTableActions(c, paletteOf(c), row, table)
			})
		})
	}, 1000, 850)
	if err := v.Click("Actions for " + table.Label()); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Count Rows", "Create", "Drop", "Drop (Cascade)", "Refresh…", "Restore…", "Backup…", "Import/Export Data…", "Reset Statistics", "ERD For Table", "Maintenance…", "Scripts", "Truncate", "View/Edit Data", "Search Objects…", "Query Tool", "Properties…"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing PostgreSQL table menu item %s", label)
		}
		if _, ok := v.Find("Icon for " + label); !ok {
			t.Fatalf("missing menu icon for %s", label)
		}
	}
	for _, label := range []string{"Backup…", "Restore…"} {
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
		if a.showTableAction || !a.tableMenuState.open {
			t.Fatalf("disabled %s executed or dismissed the menu", label)
		}
	}
	if panel, ok := v.Find("Table context menu"); !ok || panel.W > 300 || panel.H < 450 {
		t.Fatalf("table menu must fit its contents independently of the sidebar: %+v", panel)
	}
	if err := v.Click("Scripts"); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if _, ok := v.Find("Icon for CREATE Script"); !ok {
		t.Fatal("script submenu is missing its icon")
	}
	if err := v.Click("CREATE Script"); err != nil {
		t.Fatal(err)
	}
	if a.showTableAction || len(a.tabs) != 0 || !a.tableMenuState.open {
		t.Fatal("disabled CREATE script executed")
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		file, err := os.Create(filepath.Join(dir, "postgres-table-menu.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, v.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Click("Query Tool"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 || a.querySQL() != table.Preview(database.PostgreSQL) {
		t.Fatal("Query Tool did not create a safely quoted SQL draft")
	}
}

func TestPostgreSQLMenuIconsAndToolDependencies(t *testing.T) {
	a := &App{}
	var inspect func([]tableMenuItem)
	inspect = func(items []tableMenuItem) {
		for _, item := range items {
			if item.label == "" {
				continue
			}
			if _, err := assets.ControlIcons.ReadFile("icons/" + item.icon + ".svg"); err != nil {
				t.Fatalf("missing icon for %s: %v", item.label, err)
			}
			dependsOnTools := item.label == "Backup…" || item.label == "Restore…" || item.label == "CREATE Script"
			if item.disabled != dependsOnTools {
				t.Fatalf("incorrect availability for %s", item.label)
			}
			inspect(item.children)
		}
	}
	inspect(a.postgresTableItems(database.Table{Schema: "public", Name: "events"}))
}

func TestTableEditingPreservesNullAndOriginalValues(t *testing.T) {
	f := &tableDataForm{selected: 0, data: database.TableData{
		Columns: []database.Column{{Name: "id", PrimaryKey: true}, {Name: "note"}, {Name: "empty"}},
		Rows:    [][]database.TableCell{{{Text: "1", Include: true}, {Text: "NULL", Include: true}, {Null: true, Include: true}}},
	}}
	f.startRowEdit("Update")
	if !f.hasPrimaryKey() || f.values[1].Null || !f.values[2].Null {
		t.Fatal("row editor confused SQL NULL with literal NULL")
	}
	f.values[1].Text = "changed"
	if f.original[1].Text != "NULL" || f.data.Rows[0][1].Text != "NULL" {
		t.Fatal("editing mutated the original optimistic-lock values")
	}
	f.startRowEdit("Insert")
	for _, value := range f.values {
		if value.Include {
			t.Fatal("insert must use server defaults until a value is selected")
		}
	}
}

func TestTableScriptsDoNotModifyGeneratedColumns(t *testing.T) {
	table := database.Table{Schema: "public", Name: "events"}
	cols := []database.Column{{Name: "id", Identity: "a", PrimaryKey: true}, {Name: "summary", Generated: "s"}, {Name: `a"value`}}
	insert, err := tableSQLTemplate(table, "INSERT", cols)
	if err != nil || strings.Contains(insert, `"id"`) || strings.Contains(insert, `"summary"`) || !strings.Contains(insert, `"a""value"`) {
		t.Fatalf("incorrect insert draft: %s %v", insert, err)
	}
	for _, kind := range []string{"UPDATE", "DELETE"} {
		sql, err := tableSQLTemplate(table, kind, cols)
		if err != nil || !strings.HasSuffix(sql, "WHERE FALSE;") {
			t.Fatalf("%s draft needs an explicit row predicate: %s %v", kind, sql, err)
		}
	}
}

func TestNativeTableDialogsAndEditableERD(t *testing.T) {
	table := database.Table{Schema: "public", Name: "events"}
	other := database.Table{Schema: "public", Name: "users"}
	graph := database.TableDiagram{Tables: []database.DiagramTable{
		{Table: table, Columns: []database.Column{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "owner_id", Type: "integer"}}},
		{Table: other, Columns: []database.Column{{Name: "id", Type: "integer", PrimaryKey: true}}},
	}, Relations: []database.TableRelation{{Name: `owner<&"`, Source: table, Target: other}}}
	document := tableERDDrawio(graph)
	decoder := xml.NewDecoder(strings.NewReader(document))
	ids := map[string]bool{}
	edges := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid editable diagram: %v", err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "mxCell" {
			for _, attr := range element.Attr {
				if attr.Name.Local == "id" {
					if ids[attr.Value] {
						t.Fatal("duplicate diagram cell ID")
					}
					ids[attr.Value] = true
				}
				if attr.Name.Local == "edge" && attr.Value == "1" {
					edges++
				}
			}
		}
	}
	if len(ids) != 5 || edges != 1 || !strings.Contains(document, `source="table-0" target="table-1"`) {
		t.Fatalf("diagram lost its native cells or relationship: %s", document)
	}
	a := &App{showTableERD: true, tableERD: &tableERD{table: table, graph: graph}}
	v := ui.NewTester(func(c *ui.Context) { a.tableERDDialog(c, paletteOf(c)) }, 1180, 850)
	if !v.HasText("public.events") || !v.HasText("public.users") {
		t.Fatal("native ERD omitted related tables")
	}
	if err := v.Click("Close table ERD"); err != nil || a.showTableERD {
		t.Fatalf("ERD could not be closed: %v", err)
	}
	a.showTableAction = true
	a.tableAction = &tableActionForm{table: table, kind: "Truncate", action: database.TableAction{Kind: "Truncate"}}
	v = ui.NewTester(func(c *ui.Context) { a.tableActionDialog(c, paletteOf(c)) }, 1000, 800)
	if !v.HasText("Cascade") || !v.HasText("Restart owned sequences") || !v.HasText("SQL preview") {
		t.Fatal("truncate dialog omitted its options or SQL preview")
	}
}
