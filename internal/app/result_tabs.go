package app

import (
	"fmt"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

const maxResultTabs = 16

func (a *App) outputElapsed() time.Duration {
	if tab := a.currentEditor(); tab != nil {
		return tab.execution.elapsed
	}
	return a.result.Elapsed
}

type queryResultTab struct {
	view           resultView
	cursor         *database.PostgreSQLCursor
	id             int
	pinned         bool
	result         database.Result
	sql            string
	grid           ui.ListState
	selected, page int
}

func (a *App) ensureResultTab(tab *queryTab) {
	if tab == nil || len(tab.execution.results) > 0 || len(tab.execution.result.Columns) == 0 {
		return
	}
	e := &tab.execution
	e.nextResult++
	snapshot := &queryResultTab{id: e.nextResult, result: e.result, sql: e.sql, grid: e.grid, selected: e.selected, page: e.page}
	e.results = append(e.results, snapshot)
	e.activeResult = snapshot
}

func (a *App) prepareResultTab(tab *queryTab) bool {
	a.ensureResultTab(tab)
	e := &tab.execution
	target := e.activeResult
	if target == nil || target.pinned {
		if len(e.results) >= maxResultTabs {
			e.status, e.errorText, e.output = "Result limit reached", "Close a result tab before running another query (maximum 16 retained results per query tab).", true
			a.loadQueryView(tab)
			return false
		}
		e.nextResult++
		target = &queryResultTab{id: e.nextResult, selected: -1}
		e.results = append(e.results, target)
	}
	e.activeResult, e.pendingResult = target, target
	e.result, e.grid, e.selected, e.page = target.result, target.grid, target.selected, target.page
	return true
}

func (a *App) selectResultTab(tab *queryTab, snapshot *queryResultTab) {
	a.storeQueryView(tab)
	e := &tab.execution
	e.activeResult = snapshot
	e.output = false
	e.result, e.grid, e.selected, e.page = snapshot.result, snapshot.grid, snapshot.selected, snapshot.page
	a.loadQueryView(tab)
}

func (a *App) closeResultTab(tab *queryTab, target *queryResultTab) {
	e := &tab.execution
	if e.running && e.pendingResult == target {
		return
	}
	if target.cursor != nil {
		go target.cursor.Close()
	}
	a.storeQueryView(tab)
	for i, snapshot := range e.results {
		if snapshot == target {
			copy(e.results[i:], e.results[i+1:])
			e.results[len(e.results)-1] = nil
			e.results = e.results[:len(e.results)-1]
			break
		}
	}
	if e.activeResult != target {
		return
	}
	if len(e.results) > 0 {
		a.selectResultTab(tab, e.results[len(e.results)-1])
		return
	}
	e.activeResult = nil
	e.result = database.Result{}
	e.grid = ui.ListState{}
	e.page, e.selected, e.output = 0, -1, true
	a.loadQueryView(tab)
}

func (a *App) resultTabStrip(c *ui.Context, k palette) {
	tab := a.currentEditor()
	if tab != nil {
		a.storeQueryView(tab)
		a.ensureResultTab(tab)
	}
	ui.Row(c).Height(40).Padding(0, 8).Gap(6).AlignItems(ui.Center).FillWidth().Background(k.panel).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
		width, _ := c.Size()
		if width >= 1000 {
			ui.Text(c, "Database Output").FontSize(12).FontWeight(600).TextColor(k.text).Margin(0, 8)
		}
		output := ui.Row(c.Key("output-tab")).Height(30).Padding(0, 10).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Output")
		if a.output {
			output.Background(k.header).Radius(5)
		} else if output.Hovered() {
			output.Background(k.border).Radius(5)
		}
		output.Children(func() {
			ui.Icon(c, controlIcon("console")).Size(14, 14).TextColor(k.muted)
			ui.Text(c, "Output").FontSize(12).TextColor(k.text)
		})
		if output.Clicked() {
			a.output = true
			if tab != nil {
				tab.execution.output = true
			}
		}
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Height(36).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				if tab == nil || len(tab.execution.results) == 0 {
					if tabLabel(c, k, "Result 1", !a.output).Clicked() {
						a.output = false
					}
					return
				}
				for _, snapshot := range append([]*queryResultTab(nil), tab.execution.results...) {
					name := fmt.Sprintf("Result %d", snapshot.id)
					row := ui.Row(c.Key(snapshot.id)).Height(30).Margin(0, 3).Padding(0, 8).Gap(6).Radius(5).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(name).Tooltip(snapshot.sql + "\n" + snapshot.result.Elapsed.Round(time.Microsecond).String())
					if !a.output && tab.execution.activeResult == snapshot {
						row.Background(k.header).Border(1, k.border)
					} else if row.Hovered() {
						row.Background(k.border)
					}
					handled := false
					row.Children(func() {
						icon, color := "table", k.muted
						if snapshot.pinned {
							icon, color = "pin-vertical", k.accent
						}
						ui.Icon(c, controlIcon(icon)).Size(14, 14).TextColor(color)
						ui.Text(c, name).FontSize(12).TextColor(k.text)
						pending := tab.execution.running && tab.execution.pendingResult == snapshot
						if toolbarAction(c, k, "x", "Close "+name, k.muted).Size(20, 22).Disabled(pending).Clicked() {
							a.closeResultTab(tab, snapshot)
							handled = true
						}
					})
					row.PopoverMenu(func(m *ui.Menu) {
						label := "Pin " + name
						if snapshot.pinned {
							label = "Unpin " + name
						}
						pending := tab.execution.running && tab.execution.pendingResult == snapshot
						if m.Item(label).Disabled(pending || len(snapshot.result.Columns) == 0).Chosen() {
							snapshot.pinned = !snapshot.pinned
						}
					})
					if row.Clicked() && !handled {
						a.selectResultTab(tab, snapshot)
					}
				}
			})
		})
		if a.queryBusy() {
			ui.Text(c, "Running…").Margin(0, 12).FontSize(11).TextColor(k.accent)
		}
		if width >= 1000 && tab != nil {
			ui.Text(c, tab.name+" · "+a.connection).SingleLine().MaxWidth(220).FontSize(11).TextColor(k.muted)
		}
		if (tab == nil || tab.tableView == nil) && toolButton(c, k, "minus", "Hide results · Alt+8", false).Clicked() {
			a.hideResults = true
		}
	})
}

func (a *App) resultPinButton(c *ui.Context, k palette) {
	tab := a.currentEditor()
	var snapshot *queryResultTab
	if tab != nil {
		snapshot = tab.execution.activeResult
	}
	label, pinned, pending := "Pin result", false, false
	if snapshot != nil {
		pinned = snapshot.pinned
		label = fmt.Sprintf("Pin Result %d", snapshot.id)
		if pinned {
			label = fmt.Sprintf("Unpin Result %d", snapshot.id)
		}
		pending = tab.execution.running && tab.execution.pendingResult == snapshot
	}
	button := toolButton(c, k, "pin-vertical", label, pinned).Disabled(snapshot == nil || pending || len(a.result.Columns) == 0)
	if button.Clicked() {
		snapshot.pinned = !snapshot.pinned
	}
}
