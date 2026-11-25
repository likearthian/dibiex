package app

import (
	"encoding/csv"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Presentation belongs to the retained result, never to the connection or SQL editor.
type resultView struct {
	filter, scope           string
	showFilter, showSQL     bool
	hidden                  map[int]bool
	sort                    ui.SortOrder
	selection               ui.Selection[int]
	order                   []int
	orderCount              int
	orderFilter, orderScope string
	orderSort               ui.SortOrder
}

func resultRowOrder(result database.Result, view *resultView) []int {
	if view.order != nil && view.orderCount == len(result.Rows) && view.orderFilter == view.filter && view.orderScope == view.scope && view.orderSort == view.sort {
		return view.order
	}
	rows := make([]int, 0, len(result.Rows))
	needle := strings.ToLower(view.filter)
	column, scopeErr := strconv.Atoi(view.scope)
	for i, row := range result.Rows {
		matches := needle == ""
		for j, cell := range row {
			if (scopeErr != nil || column == j) && strings.Contains(strings.ToLower(cell), needle) {
				matches = true
				break
			}
		}
		if matches {
			rows = append(rows, i)
		}
	}
	if col, err := strconv.Atoi(view.sort.Column); err == nil && col >= 0 && col < len(result.Columns) {
		// Results expose display strings, not typed values. Sort these as text.
		slices.SortStableFunc(rows, func(i, j int) int {
			order := strings.Compare(result.Rows[i][col], result.Rows[j][col])
			if view.sort.Descending {
				return -order
			}
			return order
		})
	}
	view.order, view.orderCount, view.orderFilter, view.orderScope, view.orderSort = rows, len(result.Rows), view.filter, view.scope, view.sort
	return rows
}

func resultDelimited(result database.Result, rows, columns []int, separator rune) string {
	var out strings.Builder
	w := csv.NewWriter(&out)
	w.Comma = separator
	write := func(values []string) {
		cells := make([]string, len(columns))
		for i, col := range columns {
			cells[i] = values[col]
		}
		_ = w.Write(cells)
	}
	write(result.Columns)
	for _, row := range rows {
		write(result.Rows[row])
	}
	w.Flush()
	return out.String()
}

func resultVisibleOrder(visible []int, layout ui.TableLayout) []int {
	ordered := make([]int, 0, len(visible))
	for _, id := range layout.Order {
		if col, err := strconv.Atoi(id); err == nil && slices.Contains(visible, col) && !slices.Contains(ordered, col) {
			ordered = append(ordered, col)
		}
	}
	for _, col := range visible {
		if !slices.Contains(ordered, col) {
			ordered = append(ordered, col)
		}
	}
	return ordered
}

func resultSelectedBlock(grid *ui.ListState, view *resultView, rows, visible []int, start int) ([]int, []int) {
	selected := []int{}
	for row := 0; row < min(100, len(rows)-start); row++ {
		if view.selection.Has(row) {
			selected = append(selected, rows[start+row])
		}
	}
	cols := resultVisibleOrder(visible, grid.Columns)
	if !grid.Cells.All {
		anchor, cursor := -1, -1
		for i, col := range cols {
			if strconv.Itoa(col) == grid.Cells.Anchor {
				anchor = i
			}
			if strconv.Itoa(col) == grid.Cells.Cursor {
				cursor = i
			}
		}
		if anchor < 0 || cursor < 0 {
			return nil, nil
		}
		cols = cols[min(anchor, cursor) : max(anchor, cursor)+1]
	}
	return selected, cols
}

func (a *App) resetResultSelection(v *resultView) {
	layout := a.grid.Columns
	a.grid = ui.ListState{Columns: layout}
	v.selection.Clear()
	a.selected = -1
}

func resultColumnIcon(result database.Result, index int) string {
	if index >= len(result.ColumnTypes) {
		return "type-unknown"
	}
	typ := strings.TrimSpace(result.ColumnTypes[index])
	if strings.EqualFold(typ, "UNSIGNED BIG INT") {
		return "type-number"
	}
	return columnTypeIcon(database.Column{Type: typ})
}

func (a *App) resultGrid(c *ui.Context, k palette) {
	tab := a.currentEditor()
	var snapshot *queryResultTab
	if tab != nil {
		snapshot = tab.execution.activeResult
	}
	var fallback resultView
	v := &fallback
	if snapshot != nil {
		v = &snapshot.view
	}
	if v.order != nil && (v.orderFilter != v.filter || v.orderScope != v.scope || v.orderSort != v.sort) {
		a.page = 0
		a.resetResultSelection(v)
	}
	rows := resultRowOrder(a.result, v)
	columns := []ui.TableColumn{{ID: "row-number", Title: "#", Width: 44, MinWidth: 36, Fixed: true, RowHeader: true}}
	visible := []int{}
	alignments := []ui.Align{ui.Start}
	for i, name := range a.result.Columns {
		if !v.hidden[i] {
			visible = append(visible, i)
			icon := resultColumnIcon(a.result, i)
			align := ui.Start
			if icon == "type-number" {
				align = ui.End
			}
			alignments = append(alignments, align)
			columns = append(columns, ui.TableColumn{ID: strconv.Itoa(i), Title: name, Width: 170, MinWidth: 70, Sortable: true})
		}
	}
	copyColumns := resultVisibleOrder(visible, a.grid.Columns)
	ui.ScrollHorizontal(c).Height(36).FillWidth().Background(k.panel).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
		ui.Row(c).Height(36).Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, controlIcon("table")).Size(17, 17).TextColor(k.muted).Margin(0, 5)
			toolbarDivider(c, k)
			if toolbarAction(c, k, "refresh", "Rerun result query", k.muted).Disabled(a.queryBusy() || a.client == nil || snapshot == nil || snapshot.sql == "").Clicked() {
				a.executionLine = 1
				a.runSQL(snapshot.sql, false)
			}
			if toolbarAction(c, k, "square", "Stop result query", k.muted).Disabled(!a.queryBusy()).Clicked() {
				a.stopQuery()
			}
			toolbarDivider(c, k)
			a.resultPinButton(c, k)
			if toolButton(c, k, "search", "Filter loaded rows", v.showFilter).Clicked() {
				v.showFilter = !v.showFilter
			}
			if toolButton(c, k, "sql-file", "Show result SQL", v.showSQL).Disabled(snapshot == nil).Clicked() {
				v.showSQL = !v.showSQL
			}
			toolbarAction(c, k, "settings", "Result columns", k.muted).PopoverMenu(func(m *ui.Menu) {
				if m.Item("Show all columns").Chosen() {
					v.hidden = nil
				}
				for i, name := range a.result.Columns {
					label := "Hide " + name
					if v.hidden[i] {
						label = "Show " + name
					}
					if m.Item(label).Disabled(!v.hidden[i] && len(visible) <= 1).Chosen() {
						if v.hidden == nil {
							v.hidden = map[int]bool{}
						}
						v.hidden[i] = !v.hidden[i]
						a.resetResultSelection(v)
					}
				}
				if m.Item("Reset column layout").Chosen() {
					a.grid.Columns = ui.TableLayout{}
				}
				if m.Item("Clear sorting").Chosen() {
					v.sort = ui.SortOrder{}
					a.page = 0
					a.resetResultSelection(v)
				}
			})
			toolbarDivider(c, k)
			if ui.Button(c, "Export Data…").Label("Export Data").Disabled(len(visible) == 0).Clicked() {
				a.openResultExport(snapshot, rows, copyColumns)
			}
		})
	})
	if v.showFilter {
		sectionBar(c, k).Children(func() {
			beforeFilter, beforeScope := v.filter, v.scope
			label := "All columns"
			if i, err := strconv.Atoi(v.scope); err == nil && i >= 0 && i < len(a.result.Columns) {
				label = a.result.Columns[i]
			}
			ui.Button(c, label).MaxWidth(150).Label("Filter column").PopoverMenu(func(m *ui.Menu) {
				if m.Item("All columns").Chosen() {
					v.scope = ""
				}
				for i, name := range a.result.Columns {
					if m.Item(name).Chosen() {
						v.scope = strconv.Itoa(i)
					}
				}
			})
			ui.TextInput(c.Key("result-filter"), &v.filter).Grow(1).MinWidth(0).Placeholder("Contains text · loaded rows only").Label("Filter result rows")
			if toolbarAction(c, k, "x", "Clear result filter", k.muted).Clicked() {
				v.filter, v.scope = "", ""
			}
			if beforeFilter != v.filter || beforeScope != v.scope {
				a.page = 0
				a.resetResultSelection(v)
			}
		})
	}
	if v.showSQL && snapshot != nil {
		ui.Scroll(c).FillWidth().Height(76).Background(k.panel).Children(func() {
			ui.Text(c, snapshot.sql).Padding(8).Font("JetBrains Mono").FontSize(12).TextColor(k.text)
		})
	}
	if snapshot != nil && snapshot.cursor != nil {
		ui.ScrollHorizontal(c).FillWidth().Height(36).Children(func() {
			ui.Row(c).Padding(2, 8).Gap(8).AlignItems(ui.Center).Children(func() { a.cursorResultControls(c, k, tab, snapshot) })
		})
	}
	pages := max(1, (len(rows)+99)/100)
	a.page = max(0, min(a.page, pages-1))
	start := a.page * 100
	if len(a.result.Columns) == 0 {
		ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, controlIcon("table")).Size(26, 26).TextColor(k.muted)
			ui.Text(c, "No result set").FontSize(14).TextColor(k.text)
			ui.Text(c, "Run a query to view rows here.").FontSize(12).TextColor(k.muted)
		})
	} else {
		a.grid.Selected, a.grid.Selection, a.grid.CellSelect, a.grid.Sort = &a.selected, &v.selection, true, &v.sort
		a.grid.HeaderMenu = func(m *ui.Menu, col int) {
			if col <= 0 || col > len(visible) {
				return
			}
			id := visible[col-1]
			if m.Item("Filter this column").Chosen() {
				v.showFilter, v.scope = true, strconv.Itoa(id)
				a.page = 0
				a.resetResultSelection(v)
			}
			if m.Item("Hide column").Disabled(len(visible) <= 1).Chosen() {
				if v.hidden == nil {
					v.hidden = map[int]bool{}
				}
				v.hidden[id] = true
				a.resetResultSelection(v)
			}
		}
		a.grid.CellMenu = func(m *ui.Menu, row, col int) {
			if start+row >= len(rows) {
				return
			}
			index := rows[start+row]
			if m.Item("Copy selected cells as TSV").Disabled(v.selection.Len() == 0).Chosen() {
				selectedRows, selectedCols := resultSelectedBlock(&a.grid, v, rows, visible, start)
				if len(selectedRows) > 0 && len(selectedCols) > 0 {
					mygo.Clipboard.WriteText(resultDelimited(a.result, selectedRows, selectedCols, '\t'))
				}
			}
			if col > 0 && col <= len(visible) && m.Item("Copy cell").Chosen() {
				mygo.Clipboard.WriteText(a.result.Rows[index][visible[col-1]])
			}
			if m.Item("Copy row as TSV").Chosen() {
				mygo.Clipboard.WriteText(resultDelimited(a.result, []int{index}, copyColumns, '\t'))
			}
		}
		prefs := a.ensureWorkspace().resultSettings.current
		font, size, height := prefs.appearance()
		padX, padY := c.Theme().Space(2.5), c.Theme().Space(1.5)
		a.grid.HeaderCell = func(cell ui.Element, col int) {
			if col <= 0 || col > len(visible) {
				return
			}
			index := visible[col-1]
			datatype := "Unknown"
			if index < len(a.result.ColumnTypes) {
				if name := strings.TrimSpace(a.result.ColumnTypes[index]); name != "" {
					datatype = name
				}
			}
			cell.Tooltip(a.result.Columns[index] + " · " + datatype)
		}
		ui.Table(c.Key(fmt.Sprintf("results:%p", snapshot)), &a.grid, columns, min(100, len(rows)-start), func(row, col int) {
			index := rows[start+row]
			value, color := fmt.Sprint(index+1), k.muted
			if col > 0 {
				value, color = a.result.Rows[index][visible[col-1]], k.text
			}
			display := prefs.format(value)
			ui.Row(c).FillWidth().Height(height).Justify(alignments[col]).AlignItems(ui.Center).Draw(func(p *ui.Painter, r ui.Rect) {
				if prefs.HideGridLines {
					return
				}
				p.Fill(ui.Rect{X: r.X + r.W + padX - 1, Y: r.Y - padY, W: 1, H: r.H + 2*padY}, k.border, 0)
				p.Fill(ui.Rect{X: r.X - padX, Y: r.Y + r.H + padY - 1, W: r.W + 2*padX, H: 1}, k.border, 0)
			}).Children(func() {
				ui.Text(c, display).SingleLine().Tooltip(display).Font(font).FontSize(size).TextColor(color)
			})
		}).Grow(1).MinHeight(0)
	}
	sectionBar(c, k).Children(func() {
		count := fmt.Sprintf("%d rows", len(a.result.Rows))
		if v.filter != "" {
			count = fmt.Sprintf("%d / %d rows", len(rows), len(a.result.Rows))
		}
		ui.Text(c, count).FontSize(11).TextColor(k.text)
		if a.result.Truncated {
			ui.Text(c, "Limit reached").FontSize(11).TextColor(k.muted)
		}
		if a.result.Elapsed > 0 {
			ui.Text(c, a.result.Elapsed.Round(time.Microsecond).String()).FontSize(11).TextColor(k.muted)
		}
		ui.Spacer(c)
		if toolbarAction(c, k, "chevron-right", "Previous results page", k.muted).Rotate(180).Disabled(a.page == 0).Clicked() {
			a.page--
			a.resetResultSelection(v)
		}
		ui.Text(c, fmt.Sprintf("%d / %d · 100/page", a.page+1, pages)).FontSize(11).TextColor(k.muted)
		if toolbarAction(c, k, "chevron-right", "Next results page", k.muted).Disabled(a.page+1 >= pages).Clicked() {
			a.page++
			a.resetResultSelection(v)
		}
	})
}
