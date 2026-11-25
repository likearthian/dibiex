package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type tableDataForm struct {
	table                                 database.Table
	mode, filter, errorText, status, edit string
	data                                  database.TableData
	grid                                  ui.ListState
	selected, page                        int
	busy, confirmed                       bool
	cancel                                context.CancelFunc
	original, values                      []database.TableCell
}

func (a *App) openTableData(t database.Table, mode string) {
	if a.client == nil || a.client.Engine() != database.PostgreSQL {
		return
	}
	a.activateConnection()
	f := &tableDataForm{table: t, mode: mode, selected: -1}
	a.tableData, a.showTableData = f, true
	if mode != "Filtered Rows…" {
		a.reloadTableData(f, "")
	}
}

func (a *App) reloadTableData(f *tableDataForm, change string) {
	if f.busy || a.win == nil || a.client == nil {
		return
	}
	f.busy, f.errorText = true, ""
	ctx, cancel := context.WithTimeout(a.connectionContext(), 60*time.Second)
	f.cancel = cancel
	client, win := a.client, a.win
	columns := append([]database.Column(nil), f.data.Columns...)
	original, values := append([]database.TableCell(nil), f.original...), append([]database.TableCell(nil), f.values...)
	page, filter, mode, table := f.page, f.filter, f.mode, f.table
	go func() {
		defer cancel()
		session, err := client.NewQuerySession(ctx)
		var data database.TableData
		applied := false
		if err == nil {
			defer session.Close()
			if change != "" {
				err = session.ChangePostgreSQLRow(ctx, table, columns, original, values, change)
				applied = err == nil
			}
			if err == nil {
				offset := page * 100
				if mode == "First 100 Rows" || mode == "Last 100 Rows" {
					offset = 0
				}
				data, err = session.ReadPostgreSQLTablePage(ctx, table, 100, offset, filter, mode == "Last 100 Rows")
			}
		}
		win.Update(func() {
			if a.closed || a.client != client || a.tableData != f {
				return
			}
			f.busy, f.cancel = false, nil
			if applied {
				f.edit = ""
				f.values, f.original = nil, nil
				a.invalidateMetadata()
			}
			if err != nil {
				f.errorText = err.Error()
				if applied {
					f.status = change + " completed; reload failed. Refresh to see current rows."
					f.selected = -1
				}
				return
			}
			f.data, f.selected, f.grid = data, -1, ui.ListState{}
			f.edit = ""
			f.values, f.original = nil, nil
			f.status = fmt.Sprintf("%d rows on page %d", len(data.Rows), f.page+1)
			if change != "" {
				f.status = change + " completed · " + f.status
			}
		})
	}()
}

func (f *tableDataForm) hasPrimaryKey() bool {
	for _, col := range f.data.Columns {
		if col.PrimaryKey {
			return true
		}
	}
	return false
}
func (f *tableDataForm) startRowEdit(kind string) {
	f.edit, f.confirmed = kind, false
	f.errorText = ""
	if kind == "Insert" {
		f.values = make([]database.TableCell, len(f.data.Columns))
		f.original = nil
		return
	}
	if f.selected < 0 || f.selected >= len(f.data.Rows) {
		f.edit = ""
		return
	}
	f.original = append([]database.TableCell(nil), f.data.Rows[f.selected]...)
	f.values = append([]database.TableCell(nil), f.original...)
}

func (a *App) tableDataDialog(c *ui.Context, k palette) {
	if !a.showTableData || a.tableData == nil {
		return
	}
	f := a.tableData
	width, height := c.Size()
	ui.DialogBase(c.Key("table-data-dialog"), &a.showTableData, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(min(float32(1150), width-32)).Height(min(float32(760), height-40)).Radius(10).Clip().Background(k.panel).Border(1, k.border).Children(func() {
			ui.Row(c).FillWidth().Height(44).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, controlIcon("table")).Size(16, 16).TextColor(k.accent)
				ui.Text(c, "View/Edit Data · "+f.table.Label()).SingleLine().Grow(1).MinWidth(0).FontSize(15).TextColor(k.text)
				if ui.Button(c, "Close").Label("Close table data").Disabled(f.busy).Clicked() {
					a.showTableData = false
				}
			})
			if f.edit != "" {
				a.tableRowEditor(c, k, f)
			} else {
				ui.Row(c).FillWidth().Padding(4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key("table-data-filter"), &f.filter).Grow(1).MinWidth(0).Placeholder("SQL WHERE expression (optional)").Label("Table data filter").Disabled(f.busy)
					if ui.Button(c, "Apply").Disabled(f.busy).Clicked() {
						f.page = 0
						a.reloadTableData(f, "")
					}
					if ui.Button(c, "Refresh").Disabled(f.busy).Clicked() {
						a.reloadTableData(f, "")
					}
				})
				ui.Row(c).FillWidth().Padding(4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.Button(c, "Add row").Disabled(f.busy || len(f.data.Columns) == 0).Clicked() {
						f.startRowEdit("Insert")
					}
					if ui.Button(c, "Edit row").Disabled(f.busy || f.selected < 0 || !f.hasPrimaryKey()).Clicked() {
						f.startRowEdit("Update")
					}
					if ui.Button(c, "Delete row").Disabled(f.busy || f.selected < 0 || !f.hasPrimaryKey()).Clicked() {
						f.startRowEdit("Delete")
					}
					ui.Spacer(c)
					if ui.Button(c, "‹").Label("Previous table data page").Disabled(f.busy || f.page == 0).Clicked() {
						f.page--
						a.reloadTableData(f, "")
					}
					ui.Text(c, fmt.Sprintf("Page %d · 100/page", f.page+1)).FontSize(11).TextColor(k.muted)
					if ui.Button(c, "›").Label("Next table data page").Disabled(f.busy || !f.data.Truncated || f.mode == "First 100 Rows" || f.mode == "Last 100 Rows").Clicked() {
						f.page++
						a.reloadTableData(f, "")
					}
				})
				if !f.hasPrimaryKey() && len(f.data.Columns) > 0 {
					ui.Text(c, "This table has no primary key. Updating and deleting rows is disabled.").Margin(4, 12).FontSize(11).TextColor(k.muted)
				}
				cols := []ui.TableColumn{{ID: "#", Title: "#", Width: 45, MinWidth: 35}}
				for i, col := range f.data.Columns {
					label := col.Name
					if col.PrimaryKey {
						label = "⌑ " + label
					}
					cols = append(cols, ui.TableColumn{ID: fmt.Sprint(i), Title: label, Width: 160, MinWidth: 70})
				}
				f.grid.Selected = &f.selected
				ui.Table(c.Key("table-data-grid"), &f.grid, cols, len(f.data.Rows), func(row, col int) {
					text := fmt.Sprint(f.page*100 + row + 1)
					color := k.text
					if col > 0 {
						value := f.data.Rows[row][col-1]
						text = value.Text
						if value.Null {
							text = "NULL"
							color = k.muted
						}
					}
					ui.Text(c, text).SingleLine().Font("JetBrains Mono").FontSize(12).TextColor(color)
				}).Grow(1).MinHeight(0)
			}
			ui.Column(c).FillWidth().Padding(8, 12).Gap(5).Children(func() {
				if f.errorText != "" {
					ui.Text(c, f.errorText).MaxHeight(80).FontSize(11).TextColor(k.danger)
				}
				if f.status != "" {
					ui.Text(c, f.status).FontSize(11).TextColor(k.muted)
				}
				if f.busy {
					ui.Row(c).FillWidth().Gap(8).Children(func() {
						ui.Text(c, "Working…").FontSize(11).TextColor(k.muted)
						if ui.Button(c, "Stop").Clicked() && f.cancel != nil {
							f.cancel()
						}
					})
				}
			})
		})
	})
	if f.busy {
		a.showTableData = true
	}
}

func (a *App) tableRowEditor(c *ui.Context, k palette, f *tableDataForm) {
	ui.Text(c, f.edit+" row").Margin(8, 16).FontSize(14).FontWeight(600).TextColor(k.text)
	if f.edit == "Delete" {
		ui.Column(c).Grow(1).FillWidth().Padding(16).Gap(12).Children(func() {
			ui.Text(c, "Delete the selected row from "+f.table.Label()+"?").TextColor(k.text)
			ui.Checkbox(c, &f.confirmed, "Delete this row").Disabled(f.busy)
		})
	} else {
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Padding(12, 16).Gap(10).Disabled(f.busy).Children(func() {
				for i, col := range f.data.Columns {
					if col.Generated != "" || col.Identity != "" {
						ui.Text(c, col.Name+" · generated by PostgreSQL").FontSize(11).TextColor(k.muted)
						continue
					}
					value := &f.values[i]
					ui.Column(c.Key(col.Name)).FillWidth().Gap(5).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, col.Name+" · "+col.Type).Grow(1).FontSize(12).TextColor(k.text)
							if f.edit == "Insert" {
								ui.Checkbox(c, &value.Include, "Set value")
							}
							ui.Checkbox(c, &value.Null, "NULL").Disabled(!value.Include || col.Nullable == "NO")
						})
						ui.TextInput(c, &value.Text).FillWidth().Label("Row value " + col.Name).Disabled(!value.Include || value.Null)
					})
				}
			})
		})
	}
	ui.Row(c).FillWidth().Padding(10, 16).Gap(8).Children(func() {
		ui.Spacer(c)
		if ui.Button(c, "Cancel").Disabled(f.busy).Clicked() {
			f.edit = ""
		}
		if ui.PrimaryButton(c, "Apply").Label("Apply row change").Disabled(f.busy || f.edit == "Delete" && !f.confirmed).Clicked() {
			a.reloadTableData(f, strings.TrimSpace(f.edit))
		}
	})
}
