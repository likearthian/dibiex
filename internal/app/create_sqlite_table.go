package app

import (
	"context"
	"fmt"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type sqliteTableDesigner struct {
	definition               database.SQLiteTable
	tab, errorText           string
	columnIDs, constraintIDs []int
	nextID                   int
	saving                   bool
}

func (f *sqliteTableDesigner) addColumn() {
	f.nextID++
	f.columnIDs = append(f.columnIDs, f.nextID)
	f.definition.Columns = append(f.definition.Columns, database.SQLiteColumn{Type: "TEXT", Collation: "Default"})
}

func (a *App) openSQLiteTableDesigner() {
	a.activateConnection()
	a.sqliteDesigner = &sqliteTableDesigner{tab: "General"}
	a.tableDesigner, a.showCreateTable = nil, true
}

func (a *App) createSQLiteTableDialog(c *ui.Context, k palette) {
	f := a.sqliteDesigner
	width, height := c.Size()
	ui.DialogBase(c.Key("create-sqlite-table-dialog"), &a.showCreateTable, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(min(float32(1040), width-40)).Height(min(float32(720), height-48)).Radius(10).Clip().Background(k.panel).Border(1, k.border).Label("Create SQLite table").Children(func() {
			ui.Row(c).FillWidth().Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
				ui.Icon(c, controlIcon("table")).Size(18, 18).TextColor(k.accent)
				ui.Text(c, "Create table").FontSize(16).FontWeight(600).TextColor(k.text)
				ui.Text(c, a.workspaceName()+" · SQLite").SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted)
				if toolbarAction(c, k, "x", "Close table designer", k.muted).Disabled(f.saving).Clicked() {
					a.showCreateTable = false
				}
			})
			ui.Row(c).FillWidth().Height(42).Padding(4, 10).Gap(4).Children(func() {
				for _, tab := range []string{"General", "Columns", "Constraints", "SQL"} {
					if tabLabel(c, k, tab, f.tab == tab).Label("Table designer " + tab).Clicked() {
						f.tab = tab
					}
				}
			})
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Background(k.editor).Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Padding(22).Gap(16).Disabled(f.saving).Children(func() {
					switch f.tab {
					case "General":
						designerField(c, k, "Name *", func() { designerInput(c, "Table name", &f.definition.Name) })
						designerField(c, k, "Database", func() { ui.Text(c, "main").TextColor(k.text) })
						ui.Checkbox(c, &f.definition.Strict, "STRICT table")
						ui.Text(c, "STRICT accepts INT, INTEGER, REAL, TEXT, BLOB and ANY column types.").FontSize(11).TextColor(k.muted)
						ui.Checkbox(c, &f.definition.WithoutRowID, "WITHOUT ROWID")
						ui.Text(c, "WITHOUT ROWID requires a primary key and cannot use AUTOINCREMENT.").FontSize(11).TextColor(k.muted)
					case "Columns":
						a.sqliteDesignerColumns(c, k, f)
					case "Constraints":
						a.sqliteDesignerConstraints(c, k, f)
					case "SQL":
						if sql, err := f.definition.SQL(); err == nil {
							inspectorCode(c, k, "SQL preview", sql)
						} else {
							ui.Text(c, err.Error()).TextColor(k.muted)
						}
					}
				})
			})
			ui.Column(c).FillWidth().Padding(10, 16).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(k.border).Children(func() {
				_, validation := f.definition.SQL()
				message := f.errorText
				if message == "" && validation != nil {
					message = validation.Error()
				}
				if message != "" {
					ui.Text(c, message).MaxHeight(60).FontSize(11).TextColor(k.danger)
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					text := "SQLite · main"
					if f.saving {
						text = "Creating table…"
					}
					ui.Text(c, text).FontSize(11).TextColor(k.muted)
					ui.Spacer(c)
					if ui.Button(c, "Close").Label("Close create table").Disabled(f.saving).Clicked() {
						a.showCreateTable = false
					}
					if ui.Button(c, "Reset").Label("Reset table designer").Disabled(f.saving).Clicked() {
						a.openSQLiteTableDesigner()
					}
					if ui.PrimaryButton(c, "Save").Label("Save new table").Disabled(f.saving || validation != nil).Clicked() {
						a.saveSQLiteTable(f)
					}
				})
			})
		})
	})
	if f.saving {
		a.showCreateTable = true
	}
}

func (a *App) sqliteDesignerColumns(c *ui.Context, k palette, f *sqliteTableDesigner) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, fmt.Sprintf("Columns · %d", len(f.definition.Columns))).FontWeight(600).TextColor(k.text)
		ui.Spacer(c)
		if ui.Button(c, "+ Add column").Label("Add table column").Clicked() {
			f.addColumn()
		}
	})
	ui.Text(c, "Primary-key checkboxes form one composite key. INTEGER PRIMARY KEY generates row IDs automatically; AUTOINCREMENT additionally prevents their reuse.").FontSize(11).TextColor(k.muted)
	remove := -1
	for i := range f.definition.Columns {
		col := &f.definition.Columns[i]
		ui.Column(c.Key(f.columnIDs[i])).FillWidth().MinWidth(0).Padding(12).Gap(10).Radius(6).Border(1, k.border).Background(k.panel).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("name"), &col.Name).Grow(1).MinWidth(0).Placeholder("Column name").Label(fmt.Sprintf("Column %d name", i+1))
				ui.TextInput(c.Key("type"), &col.Type).Grow(1).MinWidth(0).Placeholder("Data type").Label(fmt.Sprintf("Column %d type", i+1))
				toolbarAction(c, k, "chevron-down", fmt.Sprintf("Choose column %d data type", i+1), k.muted).PopoverMenuButton(func(m *ui.Menu) {
					types := []string{"INTEGER", "REAL", "TEXT", "BLOB", "NUMERIC", "VARCHAR(255)", "DECIMAL(18,2)", "BOOLEAN", "DATE", "DATETIME"}
					if f.definition.Strict {
						types = []string{"INT", "INTEGER", "REAL", "TEXT", "BLOB", "ANY"}
					}
					for _, typ := range types {
						if m.Item(typ).Chosen() {
							col.Type = typ
						}
					}
				})
				if toolbarAction(c, k, "x", fmt.Sprintf("Remove column %d", i+1), k.muted).Clicked() {
					remove = i
				}
			})
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &col.NotNull, "Not null").Label(fmt.Sprintf("Column %d not null", i+1))
				ui.Checkbox(c, &col.PrimaryKey, "Primary key").Label(fmt.Sprintf("Column %d primary key", i+1))
				ui.Checkbox(c, &col.Unique, "Unique").Label(fmt.Sprintf("Column %d unique", i+1))
				ui.Checkbox(c, &col.AutoIncrement, "Autoincrement").Label(fmt.Sprintf("Column %d autoincrement", i+1))
			})
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, "Default SQL expression").FontSize(11).TextColor(k.muted)
					ui.TextInput(c.Key("default"), &col.Default).FillWidth().Label(fmt.Sprintf("Column %d default", i+1))
				})
				ui.Column(c).Width(160).Gap(4).Children(func() {
					ui.Text(c, "Collation").FontSize(11).TextColor(k.muted)
					ui.Select(c.Key("collation"), &col.Collation, []string{"Default", "BINARY", "NOCASE", "RTRIM"}).FillWidth().Label(fmt.Sprintf("Column %d collation", i+1))
				})
			})
		})
	}
	if remove >= 0 {
		f.definition.Columns = append(f.definition.Columns[:remove], f.definition.Columns[remove+1:]...)
		f.columnIDs = append(f.columnIDs[:remove], f.columnIDs[remove+1:]...)
	}
}

func (a *App) sqliteDesignerConstraints(c *ui.Context, k palette, f *sqliteTableDesigner) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, "Table constraints").FontWeight(600).TextColor(k.text)
		ui.Spacer(c)
		if ui.Button(c, "+ Add constraint").Label("Add table constraint").Clicked() {
			f.nextID++
			f.constraintIDs = append(f.constraintIDs, f.nextID)
			f.definition.Constraints = append(f.definition.Constraints, database.SQLiteConstraint{Kind: "UNIQUE", OnDelete: "NO ACTION", OnUpdate: "NO ACTION"})
		}
	})
	ui.Text(c, "Enter comma-separated column names for composite keys. Foreign keys reference tables in the same database.").FontSize(11).TextColor(k.muted)
	remove := -1
	for i := range f.definition.Constraints {
		constraint := &f.definition.Constraints[i]
		ui.Column(c.Key(f.constraintIDs[i])).FillWidth().MinWidth(0).Padding(12).Gap(10).Radius(6).Border(1, k.border).Background(k.panel).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.TextInput(c.Key("name"), &constraint.Name).Grow(1).MinWidth(0).Placeholder("Name (optional)").Label(fmt.Sprintf("Constraint %d name", i+1))
				ui.Select(c.Key("kind"), &constraint.Kind, []string{"PRIMARY KEY", "UNIQUE", "FOREIGN KEY", "CHECK"}).Width(160).Label(fmt.Sprintf("Constraint %d kind", i+1))
				if toolbarAction(c, k, "x", fmt.Sprintf("Remove constraint %d", i+1), k.muted).Clicked() {
					remove = i
				}
			})
			if constraint.Kind == "CHECK" {
				ui.TextInput(c.Key("expression"), &constraint.Expression).FillWidth().Placeholder("SQL expression, e.g. amount >= 0").Label(fmt.Sprintf("Constraint %d expression", i+1))
			} else {
				ui.TextInput(c.Key("columns"), &constraint.Columns).FillWidth().Placeholder("Columns, e.g. tenant_id, id").Label(fmt.Sprintf("Constraint %d columns", i+1))
			}
			if constraint.Kind == "FOREIGN KEY" {
				ui.TextInput(c.Key("ref-table"), &constraint.ReferenceTable).FillWidth().Placeholder("Referenced table").Label(fmt.Sprintf("Constraint %d reference table", i+1))
				ui.TextInput(c.Key("ref-columns"), &constraint.ReferenceColumns).FillWidth().Placeholder("Referenced columns").Label(fmt.Sprintf("Constraint %d reference columns", i+1))
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					for _, rule := range []struct {
						name  string
						value *string
					}{{"delete", &constraint.OnDelete}, {"update", &constraint.OnUpdate}} {
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, "On "+rule.name).FontSize(11).TextColor(k.muted)
							ui.Select(c.Key(rule.name), rule.value, []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"}).FillWidth().Label(fmt.Sprintf("Constraint %d on %s", i+1, rule.name))
						})
					}
				})
			}
		})
	}
	if remove >= 0 {
		f.definition.Constraints = append(f.definition.Constraints[:remove], f.definition.Constraints[remove+1:]...)
		f.constraintIDs = append(f.constraintIDs[:remove], f.constraintIDs[remove+1:]...)
	}
}

func (a *App) saveSQLiteTable(f *sqliteTableDesigner) {
	if f.saving || a.client == nil || a.win == nil {
		return
	}
	definition := f.definition
	if _, err := definition.SQL(); err != nil {
		f.errorText = err.Error()
		return
	}
	definition.Columns = append([]database.SQLiteColumn(nil), definition.Columns...)
	definition.Constraints = append([]database.SQLiteConstraint(nil), definition.Constraints...)
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.busy, a.cancel, f.saving, f.errorText = true, cancel, true, ""
	client, win, pipeline := a.client, a.win, a.introspector
	go func() {
		defer cancel()
		err := client.CreateSQLiteTable(ctx, definition)
		var schemas []database.Schema
		var refreshErr error
		if err == nil {
			if pipeline != nil {
				pipeline.Invalidate()
				schemas, refreshErr = pipeline.Catalog(ctx, true)
			} else {
				schemas, refreshErr = client.Catalog(ctx)
			}
		}
		win.Update(func() {
			if a.closed || a.client != client || a.sqliteDesigner != f {
				return
			}
			a.busy, a.cancel, f.saving = false, nil, false
			if err != nil {
				f.errorText = err.Error()
				return
			}
			a.showCreateTable = false
			a.invalidateMetadata()
			a.status = "Created table · main." + definition.Name
			if refreshErr != nil {
				a.status += " · catalog refresh failed: " + refreshErr.Error()
				return
			}
			a.applyCatalog(schemas, false)
			for _, schema := range schemas {
				for _, table := range schema.Tables {
					if table.Schema == "main" && table.Name == definition.Name {
						a.treeClosed["schema:"+schema.ID()] = false
						a.treeClosed["group:"+schema.ID()+":"+tableGroup(table)] = false
						a.selectedTree = "table:" + table.ID()
						a.inspectTable(table)
					}
				}
			}
		})
	}()
}
