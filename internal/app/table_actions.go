package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type tableActionForm struct {
	columnEdit                                                                                 database.PostgreSQLColumnEdit
	columnName, propertyTab                                                                    string
	originalAction                                                                             database.TableAction
	loaded                                                                                     bool
	parent                                                                                     *tableActionForm
	table                                                                                      database.Table
	kind, path, tool, password, user, direction, delimiter, columns, search, errorText, status string
	action                                                                                     database.TableAction
	header, dataOnly, clean, confirm, loading, busy                                            bool
	cancel                                                                                     context.CancelFunc
	metadata                                                                                   database.TableMetadata
}

func (a *App) openTableAction(t database.Table, kind string) {
	if a.client == nil || a.client.Engine() != database.PostgreSQL {
		return
	}
	if kind == "Properties" {
		a.openTableEditDesigner(t)
		return
	}
	a.activateConnection()
	f := &tableActionForm{table: t, kind: kind, direction: "Export", delimiter: ",", header: true, dataOnly: true, user: a.client.Settings().User, action: database.TableAction{Kind: kind, Type: "text", ReferenceSchema: t.Schema, Timing: "BEFORE", Event: "INSERT", Command: "VACUUM (ANALYZE)"}}
	if credential, ok := a.credential(a.connectionLabel(), a.client.Settings()); ok {
		f.password, f.user = credential.Password, credential.User
	}
	a.tableAction, a.showTableAction = f, true
	if kind == "Search Objects" || kind == "Properties" {
		a.loadTableAction(f)
	}
}

func (a *App) loadTableAction(f *tableActionForm) {
	if a.win == nil {
		return
	}
	f.loading = true
	f.loaded, f.errorText = false, ""
	client, win, parent := a.client, a.win, a.connectionContext()
	kind, columnName, table := f.kind, f.columnName, f.table
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	f.cancel = cancel
	go func() {
		defer cancel()
		var err error
		var metadata database.TableMetadata
		var properties database.TableAction
		var column database.PostgreSQLColumnEdit
		if kind == "Edit column" {
			column, err = client.PostgreSQLColumnDefinition(ctx, table, columnName)
		} else if kind == "Properties" {
			properties, err = client.PostgreSQLProperties(ctx, table)
			if err == nil {
				metadata.Columns, err = client.PostgreSQLColumns(ctx, table)
			}
		} else {
			metadata, err = client.TableMetadata(ctx, table)
		}
		win.Update(func() {
			if a.closed || a.client != client || a.tableAction != f {
				return
			}
			f.loading = false
			f.cancel = nil
			if err != nil {
				f.errorText = err.Error()
				return
			}
			f.loaded = true
			if kind == "Edit column" {
				f.columnEdit = column
			} else if kind == "Properties" {
				f.action, f.originalAction, f.metadata = properties, properties, metadata
			} else {
				f.metadata = metadata
			}
		})
	}()
}

func (a *App) pickTableActionFile(f *tableActionForm) {
	if a.win == nil {
		return
	}
	input := f.kind == "Restore" || f.kind == "Import/Export Data" && f.direction == "Import"
	ext, title := "dump", "PostgreSQL archive"
	if f.kind == "Import/Export Data" {
		ext, title = "csv", "CSV data"
	}
	filters := []mygo.FileFilter{{Name: title, Extensions: []string{ext}}, {Name: "All files", Extensions: []string{"*"}}}
	if input {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: f.kind, Filters: filters})
		if err != nil {
			f.errorText = err.Error()
		} else if len(paths) > 0 {
			f.path = paths[0]
		}
	} else {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: f.kind, DefaultPath: f.table.Name + "." + ext, Filters: filters})
		if err != nil {
			f.errorText = err.Error()
		} else if path != "" {
			f.path = path
		}
	}
}

func (a *App) tableActionDialog(c *ui.Context, k palette) {
	if !a.showTableAction || a.tableAction == nil {
		return
	}
	f := a.tableAction
	width, height := c.Size()
	ui.DialogBase(c.Key("table-action-dialog"), &a.showTableAction, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(min(float32(820), width-40)).Height(min(float32(650), height-48)).Padding(0).Radius(10).Clip().Background(k.panel).Border(1, k.border).Children(func() {
			ui.Row(c).FillWidth().Height(46).Padding(0, 16).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, controlIcon("table")).Size(16, 16).TextColor(k.accent)
				title := f.kind
				if title == "Properties" {
					title = "Edit table"
				}
				if title == "Edit column" {
					title += " " + f.columnName
				}
				ui.Text(c, title+" · "+f.table.Label()).SingleLine().Grow(1).FontSize(15).FontWeight(600).TextColor(k.text)
			})
			ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Background(k.editor).Children(func() {
				ui.Column(c).FillWidth().Padding(18).Gap(12).Disabled(f.busy || f.loading || (f.kind == "Properties" || f.kind == "Edit column") && !f.loaded).Children(func() { a.tableActionFields(c, k, f) })
			})
			ui.Column(c).FillWidth().Padding(12, 16).Gap(8).Children(func() {
				if f.errorText != "" {
					ui.Text(c, f.errorText).MaxHeight(90).FontSize(11).TextColor(k.danger)
				}
				if (f.kind == "Properties" || f.kind == "Edit column") && !f.loaded && !f.loading {
					if ui.Button(c, "Retry loading definition").Clicked() {
						a.loadTableAction(f)
					}
				}
				if f.status != "" {
					ui.Text(c, f.status).MaxHeight(50).FontSize(11).TextColor(k.muted)
				}
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.Spacer(c)
					if f.busy {
						if ui.Button(c, "Stop").Label("Stop table action").Clicked() && f.cancel != nil {
							f.cancel()
						}
					} else {
						closeLabel := "Close"
						if f.parent != nil {
							closeLabel = "Back to table"
						}
						if ui.Button(c, closeLabel).Label("Close table action").Clicked() {
							if f.loading && f.cancel != nil {
								f.cancel()
							}
							if f.parent != nil {
								a.tableAction = f.parent
							} else {
								a.showTableAction = false
							}
						}
						if f.kind != "Search Objects" {
							label := "Execute"
							if f.kind == "Properties" || f.kind == "Edit column" {
								label = "Save"
							}
							if ui.PrimaryButton(c, label).Label("Execute table action").Disabled(f.loading || ((f.kind == "Properties" || f.kind == "Edit column") && !f.loaded) || ((f.kind == "Drop" || f.kind == "Truncate" || f.kind == "Restore" || f.kind == "Reset Statistics" || f.kind == "Import/Export Data" && f.direction == "Import") && !f.confirm)).Clicked() {
								a.executeTableAction(f)
							}
						}
					}
				})
			})
		})
	})
	if f.busy {
		a.showTableAction = true
	} else if !a.showTableAction && f.parent != nil {
		if f.cancel != nil {
			f.cancel()
		}
		a.tableAction = f.parent
		a.showTableAction = true
	} else if !a.showTableAction && f.cancel != nil {
		f.cancel()
	}
}

func (a *App) tableActionFields(c *ui.Context, k palette, f *tableActionForm) {
	field := func(label string, value *string) { connectionField(c, k, label, value, false) }
	switch f.kind {
	case "Backup", "Restore", "CREATE Script":
		if f.kind != "CREATE Script" {
			field("Archive file", &f.path)
			if ui.Button(c, "Browse…").Label("Choose table action file").Clicked() {
				a.pickTableActionFile(f)
			}
		}
		field("PostgreSQL tool executable (optional)", &f.tool)
		field("User", &f.user)
		connectionField(c, k, "Password", &f.password, true)
		ui.Text(c, "Uses pg_dump / pg_restore from PATH, or the executable specified above. Password is passed only to the child process and is not saved.").FontSize(11).TextColor(k.muted)
		if f.kind == "Restore" {
			ui.Checkbox(c, &f.dataOnly, "Data only (existing table)")
			ui.Checkbox(c, &f.clean, "Replace table definition before restore").Disabled(f.dataOnly)
			ui.Text(c, "Restore is scoped to this schema/table. Indexes and other dependent objects are not selected by pg_restore --table; restore them separately when needed.").FontSize(11).TextColor(k.muted)
			ui.Checkbox(c, &f.confirm, "Restore this archive into the selected table")
		}
	case "Import/Export Data":
		ui.Select(c, &f.direction, []string{"Export", "Import"}).FillWidth().Label("Transfer direction")
		field("CSV file", &f.path)
		if ui.Button(c, "Browse…").Label("Choose table action file").Clicked() {
			a.pickTableActionFile(f)
		}
		field("Columns (comma-separated; empty means all)", &f.columns)
		field("Delimiter", &f.delimiter)
		ui.Checkbox(c, &f.header, "Header row")
		ui.Text(c, "CSV uses PostgreSQL COPY with UTF-8 encoding and native NULL/quoting rules. Transfers are limited to 256 MiB.").FontSize(11).TextColor(k.muted)
		if f.direction == "Import" {
			ui.Checkbox(c, &f.confirm, "Import these rows into the selected table")
		}
	case "Search Objects":
		a.tableObjectSearch(c, k, f)
	case "Properties":
		ui.Row(c).Gap(6).Children(func() {
			for _, tab := range []string{"General", "Columns"} {
				if tabLabel(c, k, tab, f.propertyTab == tab || f.propertyTab == "" && tab == "General").Label("Edit table " + tab).Clicked() {
					f.propertyTab = tab
				}
			}
		})
		if f.propertyTab == "Columns" {
			a.editTableColumnList(c, k, f)
			break
		}
		field("Table name", &f.action.Name)
		field("Schema", &f.action.ReferenceSchema)
		field("Owner", &f.action.Function)
		connectionField(c, k, "Comment", &f.action.Expression, false)
		ui.Checkbox(c, &f.action.RowSecurity, "Enable row-level security")
		ui.Checkbox(c, &f.action.ForceRowSecurity, "Force row-level security")
		if ui.Button(c, "Inspect all table metadata").Clicked() {
			a.showTableAction = false
			a.inspectTable(f.table)
		}
	case "Edit column":
		a.columnEditorFields(c, k, f)
	case "Drop", "Truncate", "Reset Statistics":
		ui.Text(c, map[string]string{"Drop": "This removes the table and its data. CASCADE also removes dependent objects.", "Truncate": "This removes every row from the table. CASCADE includes referencing tables.", "Reset Statistics": "Reset the selected table's statistics counters. Your PostgreSQL role must have permission."}[f.kind]).FontSize(12).TextColor(k.text)
		if f.kind != "Reset Statistics" {
			ui.Checkbox(c, &f.action.Cascade, "Cascade")
		}
		if f.kind == "Truncate" {
			ui.Checkbox(c, &f.action.RestartIdentity, "Restart owned sequences")
		}
		ui.Checkbox(c, &f.confirm, "Apply this operation to "+f.table.Label())
	case "Maintenance":
		ui.Select(c, &f.action.Command, []string{"VACUUM", "VACUUM (ANALYZE)", "VACUUM (FULL, ANALYZE)", "ANALYZE", "REINDEX TABLE"}).FillWidth().Label("Maintenance operation")
		ui.Text(c, "VACUUM FULL and REINDEX can block concurrent access. The operation runs in its own session.").FontSize(11).TextColor(k.muted)
	default:
		field("Name", &f.action.Name)
		switch f.kind {
		case "Column":
			ui.Text(c, "Data type").FontSize(11).TextColor(k.muted)
			ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("new-column-type"), &f.action.Type).Grow(1).MinWidth(0).Label("Column data type").Tooltip("Choose a type, then edit its length or precision, for example varchar(80).")
				toolbarAction(c, k, "chevron-down", "Choose column data type", k.muted).PopoverMenuButton(func(m *ui.Menu) {
					for _, typ := range postgresColumnTypes {
						if m.Item(typ).Chosen() {
							f.action.Type = typ
						}
					}
				})
			})
			field("Default SQL expression (optional)", &f.action.Expression)
			ui.Checkbox(c, &f.action.NotNull, "Not null")
		case "Index", "Primary key", "Unique":
			field("Columns (comma-separated)", &f.action.Columns)
			if f.kind == "Index" {
				ui.Checkbox(c, &f.action.Unique, "Unique index")
			}
		case "Check", "Policy":
			field("SQL expression", &f.action.Expression)
		case "Foreign key":
			field("Columns", &f.action.Columns)
			field("Referenced schema", &f.action.ReferenceSchema)
			field("Referenced table", &f.action.ReferenceTable)
			field("Referenced columns", &f.action.ReferenceColumns)
		case "Trigger":
			ui.Select(c, &f.action.Timing, []string{"BEFORE", "AFTER"}).FillWidth().Label("Trigger timing")
			ui.Select(c, &f.action.Event, []string{"INSERT", "UPDATE", "DELETE"}).FillWidth().Label("Trigger event")
			field("Function schema", &f.action.ReferenceSchema)
			field("Existing trigger function (no arguments)", &f.action.Function)
		case "Rule":
			ui.Select(c, &f.action.Event, []string{"INSERT", "UPDATE", "DELETE"}).FillWidth().Label("Rule event")
			field("DO ALSO SQL command", &f.action.Expression)
		}
	}
	if f.loading {
		ui.Text(c, "Loading…").FontSize(12).TextColor(k.muted)
	}
	if f.kind != "Search Objects" && f.kind != "Backup" && f.kind != "Restore" && f.kind != "CREATE Script" && f.kind != "Import/Export Data" {
		if sql, err := f.editSQL(); err == nil {
			inspectorCode(c, k, "SQL preview", sql)
		} else if f.loaded && (f.kind == "Properties" || f.kind == "Edit column") {
			ui.Text(c, err.Error()).FontSize(11).TextColor(k.danger)
		}
	}
}

func (a *App) executeTableAction(f *tableActionForm) {
	if f.busy || a.client == nil || a.win == nil {
		return
	}
	if (f.kind == "Edit column" || f.kind == "Properties") && (!f.loaded || f.loading) {
		f.errorText = "Load the current definition before saving"
		return
	}
	if (f.kind == "Backup" || f.kind == "Restore" || f.kind == "Import/Export Data") && strings.TrimSpace(f.path) == "" {
		f.errorText = "Choose a file"
		return
	}
	sql := ""
	var err error
	if f.kind != "Backup" && f.kind != "Restore" && f.kind != "CREATE Script" && f.kind != "Import/Export Data" {
		sql, err = f.editSQL()
		if err != nil {
			f.errorText = err.Error()
			return
		}
	}
	f.busy, f.errorText, f.status = true, "", "Running…"
	ctx, cancel := context.WithTimeout(a.connectionContext(), 5*time.Minute)
	f.cancel = cancel
	client, win := a.client, a.win
	cfg := client.Settings()
	cfg.User = f.user
	action, path, tool, password, direction, columns, delimiter, header, dataOnly, clean := f.action, f.path, f.tool, f.password, f.direction, f.columns, f.delimiter, f.header, f.dataOnly, f.clean
	kind, table, columnEdit, originalAction := f.kind, f.table, f.columnEdit, f.originalAction
	go func() {
		defer cancel()
		var count int64
		script := ""
		var err error
		if f.kind == "Backup" || f.kind == "Restore" || f.kind == "CREATE Script" {
			if f.kind == "CREATE Script" {
				file, e := os.CreateTemp("", "dbcraft-create-*.sql")
				err = e
				if e == nil {
					path = file.Name()
					file.Close()
					defer os.Remove(path)
				}
			}
			if err == nil {
				err = database.RunPostgreSQLTool(ctx, cfg, f.table, database.PGToolOptions{Restore: f.kind == "Restore", DataOnly: dataOnly, Clean: clean, Plain: f.kind == "CREATE Script", SchemaOnly: f.kind == "CREATE Script", Tool: tool, Path: path, Password: password})
			}
			if err == nil && f.kind == "CREATE Script" {
				script, err = readSQLFile(path)
			}
		} else {
			session, e := client.NewQuerySession(ctx)
			err = e
			if err == nil {
				defer session.Close()
				if f.kind == "Import/Export Data" {
					count, err = transferTableCSV(ctx, cancel, session, f.table, path, columns, delimiter, direction, header)
				} else if kind == "Edit column" {
					err = session.AlterPostgreSQLColumn(ctx, table, columnEdit)
				} else if kind == "Properties" {
					err = session.AlterPostgreSQLTable(ctx, table, originalAction, action)
				} else {
					_, err = session.ExecuteResult(ctx, sql)
				}
			}
		}
		win.Update(func() {
			if a.closed || a.client != client || a.tableAction != f {
				return
			}
			f.busy, f.cancel = false, nil
			f.password = ""
			if err != nil {
				f.errorText = err.Error()
				f.status = "Operation failed"
				return
			}
			f.status = f.kind + " completed"
			if f.kind == "Import/Export Data" {
				f.status = fmt.Sprintf("%s complete · %d rows", direction, count)
			}
			if script != "" {
				a.showTableAction = false
				a.sqlDraft("create-"+f.table.Name, script)
			}
			if action.Kind == "Properties" && action.Name != f.table.Name {
				f.table.Name = action.Name
			}
			if kind == "Properties" && action.ReferenceSchema != "" {
				f.table.Schema = action.ReferenceSchema
			}
			if kind == "Edit column" {
				f.columnName = columnEdit.Name
			}
			if kind == "Properties" || kind == "Edit column" {
				a.editedTableSelection(table, f.table, columnEdit.Original.Name, columnEdit.Name)
				if f.parent != nil {
					for i := range f.parent.metadata.Columns {
						col := &f.parent.metadata.Columns[i]
						if col.Name == columnEdit.Original.Name {
							col.Name, col.Type = columnEdit.Name, columnEdit.Type
						}
					}
				}
				a.loadTableAction(f)
			}
			a.status = f.status
			if f.kind != "Backup" && f.kind != "CREATE Script" {
				a.refreshCatalog(func() {
					if kind == "Properties" || kind == "Edit column" {
						a.loadTableMetadata(f.table, true)
					}
				})
			}
		})
	}()
}

func transferTableCSV(ctx context.Context, cancel context.CancelFunc, session *database.Client, t database.Table, path, columns, delimiter, direction string, header bool) (int64, error) {
	if direction == "Import" {
		file, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() || info.Size() > database.MaxTransferBytes {
			return 0, fmt.Errorf("choose a regular CSV file no larger than 256 MiB")
		}
		return session.CopyPostgreSQLCSV(ctx, t, columns, header, delimiter, io.LimitReader(file, database.MaxTransferBytes+1), nil)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "dbcraft-export-*.tmp")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	count, err := session.CopyPostgreSQLCSV(ctx, t, columns, header, delimiter, nil, &database.LimitedWriter{Writer: file, Remaining: database.MaxTransferBytes, Cancel: cancel})
	closeErr := file.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return count, os.Rename(file.Name(), path)
}

func (a *App) openTableScript(t database.Table, kind string) {
	if a.client == nil || a.win == nil {
		return
	}
	client, win, parent := a.client, a.win, a.connectionContext()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		cols, err := client.PostgreSQLColumns(ctx, t)
		script := ""
		if err == nil {
			script, err = tableSQLTemplate(t, kind, cols)
		}
		win.Update(func() {
			if a.closed || a.client != client {
				return
			}
			if err != nil {
				a.status = err.Error()
				return
			}
			a.tableQuery(t, strings.ToLower(kind), script, false)
		})
	}()
}

func (a *App) tableObjectSearch(c *ui.Context, k palette, f *tableActionForm) {
	ui.TextInput(c.Key("object-search"), &f.search).FillWidth().Placeholder("Search columns, keys, indexes, triggers, rules and policies").Label("Search table objects")
	needle := strings.ToLower(f.search)
	shown := 0
	for _, col := range f.metadata.Columns {
		if strings.Contains(strings.ToLower(col.Name), needle) {
			if ui.Button(c, col.Name+" · "+col.Type).FillWidth().Label("Inspect found column " + col.Name).Clicked() {
				a.showTableAction = false
				if a.metadata == nil {
					a.metadata = map[string]database.TableMetadata{}
				}
				a.metadata[f.table.ID()] = f.metadata
				a.inspectColumn(f.table, []string{col.Name}, col)
			}
			shown++
			if shown >= 500 {
				break
			}
		}
	}
	for _, section := range []string{"keys", "constraints", "indexes", "triggers", "rules", "policies"} {
		for index, d := range tableDefinitionItems(f.metadata, section) {
			if shown >= 500 {
				break
			}
			if strings.Contains(strings.ToLower(d.Name), needle) {
				if ui.Button(c, d.Name+" · "+section).FillWidth().Label("Inspect found object " + d.Name).Clicked() {
					a.showTableAction = false
					if a.metadata == nil {
						a.metadata = map[string]database.TableMetadata{}
					}
					a.metadata[f.table.ID()] = f.metadata
					a.inspectTableObject(f.table, section, index, d)
				}
				shown++
			}
		}
	}
	if !f.loading {
		ui.Text(c, fmt.Sprintf("%d matching objects (maximum 500)", shown)).FontSize(11).TextColor(k.muted)
	}
}
