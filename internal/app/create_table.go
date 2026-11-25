package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

type tableDesigner struct {
	edit                                    *database.PostgreSQLTableDesign
	loaded, confirmDrops                    bool
	cancel                                  context.CancelFunc
	indexIDs                                []int
	definition                              database.PostgreSQLTable
	tab, owner, tablespace, errorText, info string
	owners, tablespaces, schemas            []string
	columnIDs, constraintIDs                []int
	nextID                                  int
	loading, saving                         bool
}

func (f *tableDesigner) table() database.PostgreSQLTable {
	t := f.definition
	t.Owner, t.Tablespace = "", ""
	if f.owner != "Current role" || f.edit != nil {
		t.Owner = f.owner
	}
	if f.tablespace != "Database default" || f.edit != nil && f.edit.Definition.Tablespace == "Database default" {
		t.Tablespace = f.tablespace
	}
	return t
}

func (f *tableDesigner) addColumn() {
	f.nextID++
	f.columnIDs = append(f.columnIDs, f.nextID)
	f.definition.Columns = append(f.definition.Columns, database.PostgreSQLColumn{Type: "text", Identity: "None"})
}

func (a *App) openTableDesigner(schema string) {
	if a.busy || a.client == nil {
		return
	}
	if a.client.Engine() == database.SQLite {
		a.openSQLiteTableDesigner()
		return
	}
	if a.client.Engine() != database.PostgreSQL {
		return
	}
	a.sqliteDesigner = nil
	a.activateConnection()
	f := &tableDesigner{definition: database.PostgreSQLTable{Schema: schema, PartitionMethod: "RANGE", Autovacuum: "Default"}, tab: "General", owner: "Current role", tablespace: "Database default", owners: []string{"Current role"}, tablespaces: []string{"Database default"}, schemas: []string{schema}}
	for _, s := range a.schemas {
		if s.Name != schema {
			f.schemas = append(f.schemas, s.Name)
		}
	}
	a.tableDesigner, a.showCreateTable = f, true
	a.loadTableDesignerChoices(f)
}

func (a *App) loadTableDesignerChoices(f *tableDesigner) {
	if a.win == nil {
		return
	}
	f.loading = true
	client, win, parent := a.client, a.win, a.connectionContext()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		result, err := client.Query(ctx, `SELECT 'Owner', rolname FROM (SELECT rolname FROM pg_roles ORDER BY rolname LIMIT 400) r UNION ALL SELECT 'Tablespace', spcname FROM (SELECT spcname FROM pg_tablespace WHERE spcname <> 'pg_global' ORDER BY spcname LIMIT 400) t`)
		win.Update(func() {
			if a.closed || a.client != client || a.tableDesigner != f {
				return
			}
			f.loading = false
			if err != nil {
				f.info = "Could not load role and tablespace choices: " + err.Error()
				return
			}
			for _, row := range result.Rows {
				if len(row) < 2 {
					continue
				}
				if row[0] == "Owner" {
					if !containsDesignerChoice(f.owners, row[1]) {
						f.owners = append(f.owners, row[1])
					}
				} else {
					if !containsDesignerChoice(f.tablespaces, row[1]) {
						f.tablespaces = append(f.tablespaces, row[1])
					}
				}
			}
		})
	}()
}

func designerField(c *ui.Context, k palette, label string, body func()) {
	ui.Row(c).FillWidth().MinWidth(0).Gap(20).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(138).Shrink(0).FontSize(12).TextColor(k.muted)
		ui.Column(c).Grow(1).MinWidth(0).Children(body)
	})
}

func designerInput(c *ui.Context, label string, value *string) {
	ui.TextInput(c.Key(label), value).FillWidth().Label(label)
}

func (a *App) createTableDialog(c *ui.Context, k palette) {
	if a.showCreateTable && a.sqliteDesigner != nil {
		a.createSQLiteTableDialog(c, k)
		return
	}
	if !a.showCreateTable || a.tableDesigner == nil {
		return
	}
	f := a.tableDesigner
	editing := f.edit != nil
	title, dialogLabel, saveLabel := "Create table", "Create PostgreSQL table", "Save new table"
	if editing {
		title, dialogLabel, saveLabel = "Edit table", "Edit PostgreSQL table", "Save table changes"
	}
	width, height := c.Size()
	panelWidth, panelHeight := min(float32(1040), width-40), min(float32(720), height-48)
	ui.DialogBase(c.Key("create-table-dialog"), &a.showCreateTable, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(panelWidth).Height(panelHeight).Padding(0).Radius(10).Clip().Background(k.panel).Border(1, k.border).Label(dialogLabel).Children(func() {
			ui.Row(c).FillWidth().Height(48).Padding(0, 16).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
				ui.Icon(c, controlIcon("table")).Size(18, 18).TextColor(k.accent)
				ui.Text(c, title).FontSize(16).FontWeight(600).TextColor(k.text)
				ui.Text(c, a.workspaceName()).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted)
				if toolbarAction(c, k, "x", "Close table designer", k.muted).Disabled(f.saving).Clicked() {
					if f.cancel != nil {
						f.cancel()
					}
					a.showCreateTable = false
				}
			})
			ui.ScrollHorizontal(c).FillWidth().Height(42).Children(func() {
				ui.Row(c).Padding(4, 10).Gap(4).Children(func() {
					for _, tab := range []string{"General", "Columns", "Advanced", "Constraints", "Indexes", "Partitions", "Parameters", "Security", "SQL"} {
						if tabLabel(c, k, tab, f.tab == tab).Label("Table designer " + tab).Clicked() {
							f.tab = tab
						}
					}
				})
			})
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Background(k.editor).Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Padding(22).Gap(16).Disabled(f.saving || editing && !f.loaded).Children(func() { a.tableDesignerPage(c, k, f) })
			})
			ui.Column(c).FillWidth().Padding(10, 16).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(k.border).Children(func() {
				_, destructive, validation := f.statements()
				message := f.errorText
				if message == "" && validation != nil {
					message = validation.Error()
				}
				if message != "" {
					ui.Text(c, message).MaxHeight(44).FontSize(11).TextColor(k.danger)
				}
				if editing && !f.loaded && !f.loading {
					if ui.Button(c, "Retry loading table definition").Clicked() {
						a.loadTableEditDesigner(f)
					}
				}
				if editing && destructive {
					ui.Checkbox(c, &f.confirmDrops, "Apply removals shown in the SQL preview").Label("Confirm table designer removals").Disabled(f.saving)
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					if f.saving {
						status := "Creating table…"
						if editing {
							status = "Saving table changes…"
						}
						ui.Text(c, status).FontSize(11).TextColor(k.muted)
						if editing && f.cancel != nil {
							if ui.Button(c, "Stop").Label("Stop table designer save").Clicked() {
								f.cancel()
							}
						}
					} else {
						ui.Text(c, "PostgreSQL · "+f.definition.Schema).SingleLine().FontSize(11).TextColor(k.muted)
					}
					ui.Spacer(c)
					if ui.Button(c, "Close").Label("Close create table").Disabled(f.saving).Clicked() {
						if f.cancel != nil {
							f.cancel()
						}
						a.showCreateTable = false
					}
					if ui.Button(c, "Reset").Label("Reset table designer").Disabled(f.saving).Clicked() {
						if editing {
							if f.loaded {
								f.resetEdit()
							}
						} else {
							a.openTableDesigner(f.definition.Schema)
						}
					}
					if ui.PrimaryButton(c, "Save").Label(saveLabel).Disabled(f.saving || !editing && a.queryBusy() || validation != nil || editing && destructive && !f.confirmDrops).Clicked() {
						if editing {
							a.saveEditedTable(f)
						} else {
							a.saveNewTable(f)
						}
					}
				})
			})
		})
	})
	if f.saving {
		a.showCreateTable = true
	} else if !a.showCreateTable && f.cancel != nil {
		f.cancel()
	}
}

func (a *App) tableDesignerPage(c *ui.Context, k palette, f *tableDesigner) {
	d := &f.definition
	switch f.tab {
	case "General":
		designerField(c, k, "Name *", func() { designerInput(c, "Table name", &d.Name) })
		designerField(c, k, "Owner", func() {
			ui.Select(c.Key("owner"), &f.owner, f.owners).FillWidth().Disabled(f.loading).Label("Table owner")
		})
		designerField(c, k, "Schema *", func() { ui.Select(c.Key("schema"), &d.Schema, f.schemas).FillWidth().Label("Table schema") })
		designerField(c, k, "Tablespace", func() {
			ui.Select(c.Key("tablespace"), &f.tablespace, f.tablespaces).FillWidth().Disabled(f.loading).Label("Table tablespace")
		})
		designerField(c, k, "Partitioned table", func() {
			ui.Checkbox(c, &d.Partitioned, "Partitioned table").Label("Partitioned table").Disabled(f.edit != nil)
		})
		designerField(c, k, "Comment", func() { ui.TextArea(c.Key("comment"), &d.Comment).FillWidth().Height(100).Label("Table comment") })
		if f.loading {
			message := "Loading roles and tablespaces…"
			if f.edit != nil && !f.loaded {
				message = "Loading current table definition…"
			}
			ui.Text(c, message).FontSize(11).TextColor(k.muted)
		}
		if f.info != "" {
			ui.Text(c, f.info).FontSize(11).TextColor(k.muted)
		}
	case "Columns":
		a.tableDesignerColumns(c, k, f)
	case "Constraints":
		a.tableDesignerConstraints(c, k, f)
	case "Indexes":
		a.tableDesignerIndexes(c, k, f)
	case "Advanced":
		ui.Checkbox(c, &d.Unlogged, "Unlogged table").Label("Unlogged table").Disabled(f.edit != nil && d.Partitioned)
		ui.Text(c, "Unlogged tables skip write-ahead logging and are not crash-safe or replicated to standby servers.").FontSize(12).TextColor(k.muted)
	case "Partitions":
		if f.edit != nil {
			ui.Text(c, "An existing partition layout cannot be changed in place. Its current definition is preserved.").FontSize(12).TextColor(k.muted)
			if f.edit.PartitionSQL != "" {
				inspectorCode(c, k, "Partition definition", f.edit.PartitionSQL)
			}
		}
		ui.Column(c).FillWidth().Gap(16).Disabled(f.edit != nil).Children(func() {
			ui.Checkbox(c, &d.Partitioned, "Partitioned table").Label("Partitioned table")
			designerField(c, k, "Method", func() {
				ui.Select(c.Key("partition-method"), &d.PartitionMethod, []string{"RANGE", "LIST", "HASH"}).FillWidth().Label("Partition method")
			})
			designerField(c, k, "Key columns", func() { designerInput(c, "Partition columns", &d.PartitionColumns) })
			ui.Text(c, "Enter column names separated by commas. This creates the partitioned parent; create child partitions separately before inserting rows.").FontSize(12).TextColor(k.muted)
		})
	case "Parameters":
		designerField(c, k, "Fillfactor", func() { designerInput(c, "Table fillfactor", &d.Fillfactor) })
		ui.Text(c, "Optional, 10–100. Leave empty to use the server default.").FontSize(11).TextColor(k.muted)
		designerField(c, k, "Autovacuum", func() {
			ui.Select(c.Key("autovacuum"), &d.Autovacuum, []string{"Default", "true", "false"}).FillWidth().Label("Table autovacuum")
		})
		ui.Text(c, "Storage parameters apply to ordinary tables and individual partitions.").FontSize(11).TextColor(k.muted)
	case "Security":
		ui.Checkbox(c, &d.RowSecurity, "Enable row-level security").Label("Enable row-level security")
		ui.Checkbox(c, &d.ForceRowSecurity, "Force row-level security for the table owner").Label("Force row-level security")
		ui.Text(c, "When enabled without policies, row-level security denies access by default for roles subject to it. Policies and grants can be managed after creation.").FontSize(12).TextColor(k.muted)
	case "SQL":
		statements, _, err := f.statements()
		if err != nil {
			ui.Text(c, err.Error()).FontSize(12).TextColor(k.danger)
			return
		}
		label := "Create table SQL"
		if f.edit != nil {
			label = "Alter table SQL preview"
		}
		preview := "-- No changes"
		if len(statements) > 0 {
			preview = "BEGIN;\n\n" + strings.Join(statements, ";\n\n") + ";\n\nCOMMIT;"
		}
		inspectorCode(c, k, label, preview)
	}
}

func (a *App) saveNewTable(f *tableDesigner) {
	if a.queryBusy() || f.saving || a.client == nil || a.win == nil {
		return
	}
	definition := f.table()
	if _, err := definition.Statements(); err != nil {
		f.errorText = err.Error()
		return
	}
	// Snapshot editable slices before handing the definition to the worker.
	definition.Columns = append([]database.PostgreSQLColumn(nil), definition.Columns...)
	definition.Constraints = append([]database.PostgreSQLConstraint(nil), definition.Constraints...)
	definition.Indexes = append([]database.PostgreSQLIndex(nil), definition.Indexes...)
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.busy, a.cancel, f.saving, f.errorText = true, cancel, true, ""
	client, win, pipeline := a.client, a.win, a.introspector
	runner := client
	if tab := a.currentEditor(); tab != nil && tab.session != nil {
		runner = tab.session
	}
	go func() {
		defer cancel()
		err := runner.CreatePostgreSQLTable(ctx, definition)
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
			if a.closed || a.client != client || a.tableDesigner != f {
				return
			}
			a.busy, a.cancel, f.saving = false, nil, false
			if err != nil {
				f.errorText = err.Error()
				return
			}
			a.showCreateTable = false
			a.invalidateMetadata()
			if refreshErr == nil {
				a.applyCatalog(schemas, false)
				for _, schema := range schemas {
					for _, table := range schema.Tables {
						if table.Schema == definition.Schema && table.Name == definition.Name {
							a.treeClosed["schema:"+schema.ID()] = false
							a.treeClosed["group:"+schema.ID()+":"+tableGroup(table)] = false
							a.selectedTree = "table:" + table.ID()
							a.inspectTable(table)
						}
					}
				}
			}
			a.status = fmt.Sprintf("Created table · %s.%s", definition.Schema, definition.Name)
			if refreshErr != nil {
				a.status += " · catalog refresh failed: " + refreshErr.Error()
			}
			a.loadExplorerCatalog(true)
		})
	}()
}
