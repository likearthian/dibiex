package app

import (
	"strings"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func editablePostgreSQLTable(engine database.Engine, t database.Table) bool {
	return engine == database.PostgreSQL && (t.Kind == "" || t.Kind == "Tables" || t.Kind == "Partitioned tables")
}

func (a *App) openColumnEditor(t database.Table, name string) {
	if a.client == nil || !editablePostgreSQLTable(a.client.Engine(), t) {
		return
	}
	a.openTableAction(t, "Edit column")
	a.tableAction.columnName = name
	a.loadTableAction(a.tableAction)
}

func (a *App) editTableColumnList(c *ui.Context, k palette, f *tableActionForm) {
	ui.TextInput(c.Key("edit-table-column-filter"), &f.search).FillWidth().Label("Find column to edit").Placeholder("Find column…")
	for _, col := range f.metadata.Columns {
		if !strings.Contains(strings.ToLower(col.Name), strings.ToLower(f.search)) {
			continue
		}
		ui.Row(c.Key("edit-table-column:" + col.Name)).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			dataTypeIcon(c, k, col)
			ui.Text(c, col.Name).SingleLine().Grow(1).MinWidth(0).TextColor(k.text)
			ui.Text(c, col.Type).SingleLine().MinWidth(0).FontSize(11).TextColor(k.muted)
			if ui.Button(c, "Edit…").Label("Edit column " + col.Name).Clicked() {
				a.openColumnEditor(f.table, col.Name)
				if a.tableAction != f {
					a.tableAction.parent = f
				}
			}
		})
	}
}

func (a *App) columnEditorFields(c *ui.Context, k palette, f *tableActionForm) {
	e := &f.columnEdit
	field := func(label string, value *string) {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, label).FontSize(11).TextColor(k.muted)
			ui.TextInput(c.Key(label), value).FillWidth().Label("Column editor " + label)
		})
	}
	field("Column name", &e.Name)
	protected := e.Original.Generated != "" || e.Original.Identity != ""
	ui.Column(c).FillWidth().Gap(12).Disabled(protected).Children(func() {
		field("Data type", &e.Type)
		ui.Row(c).Gap(4).Children(func() {
			toolbarAction(c, k, "chevron-down", "Choose edited column type", k.muted).PopoverMenuButton(func(m *ui.Menu) {
				for _, typ := range postgresColumnTypes {
					if typ == "serial" || typ == "smallserial" || typ == "bigserial" {
						continue
					}
					if m.Item(typ).Chosen() {
						e.Type = typ
					}
				}
			})
			ui.Text(c, "Choose a type or enter a qualified custom type.").FontSize(11).TextColor(k.muted)
		})
		field("USING SQL expression (optional)", &e.Using)
		field("Default SQL expression (empty removes default)", &e.Default)
		ui.Checkbox(c, &e.NotNull, "Not null").Disabled(e.Original.PrimaryKey)
	})
	field("Comment", &e.Comment)
	if protected {
		ui.Text(c, "Generated and identity columns can be renamed and commented here; their generation settings are preserved.").FontSize(11).TextColor(k.muted)
	}
	ui.Text(c, "Type changes can rewrite data and lock the table. USING refers to the original column name. Failed changes are rolled back.").FontSize(11).TextColor(k.muted)
}

func (f *tableActionForm) editSQL() (string, error) {
	if f.kind == "Edit column" {
		return f.columnEdit.SQL(f.table)
	}
	return f.action.SQL(f.table)
}

func (a *App) editedTableSelection(before, after database.Table, oldColumn, newColumn string) {
	if a.inspectorTable.ID() == before.ID() {
		a.inspectorTable = after
	}
	if selected := a.inspectorColumn; selected != nil && selected.Table.ID() == before.ID() {
		selected.Table = after
		if len(selected.Path) == 1 && selected.Path[0] == oldColumn {
			selected.Path[0] = newColumn
			selected.Column.Name = newColumn
		}
		a.selectedTree = columnTreeKey(after, selected.Path)
		a.setObjectPath(append(append(a.tableObjectPath(after), "columns"), selected.Path...)...)
	} else if a.activeTable == before.ID() {
		a.selectedTree = "table:" + after.ID()
		a.setObjectPath(a.tableObjectPath(after)...)
	}
	if a.activeTable == before.ID() {
		a.activeTable = after.ID()
	}
	if selected := a.inspectorDefinition; selected != nil && selected.Table.ID() == before.ID() {
		selected.Table = after
	}
}
