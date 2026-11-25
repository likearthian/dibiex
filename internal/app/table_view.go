package app

import (
	"fmt"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) openTableView(table database.Table) {
	if a.client == nil || a.busy {
		return
	}
	a.activateConnection()
	engine := a.client.Engine()
	if engine == database.SQLite && table.Schema != "" && table.Schema != "main" {
		a.status = "Open this SQLite table in the SQL console that owns its session."
		return
	}
	for index, tab := range a.tabs {
		if tab.tableView != nil && tab.tableView.ID() == table.ID() {
			a.selectQueryTab(index)
			if len(tab.execution.results) == 0 {
				a.run(false)
			}
			return
		}
	}
	query := fmt.Sprintf("SELECT * FROM %s LIMIT %d", table.Reference(engine), database.MaxRows+1)
	a.addQuery(query)
	tab := a.currentEditor()
	tab.name, tab.tableView = table.Label(), &table
	a.run(false)
}

func (a *App) tableViewPane(c *ui.Context, k palette, tab *queryTab) {
	sectionBar(c, k).Children(func() {
		ui.Icon(c, controlIcon("table")).Size(16, 16).TextColor(k.muted)
		ui.Text(c, tab.tableView.Label()).SingleLine().MinWidth(0).TextColor(k.text)
		ui.Spacer(c)
		if toolbarAction(c, k, "refresh", "Refresh table data", k.accent).Disabled(a.client == nil || a.queryBusy()).Clicked() {
			a.run(false)
		}
		if toolbarAction(c, k, "square", "Stop loading table data", k.muted).Disabled(!tab.execution.running).Clicked() {
			a.stopQuery()
		}
	})
	if a.client == nil {
		ui.Text(c, "Reconnect to load table data.").Padding(12).TextColor(k.muted)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		a.resultsPane(c, k)
	})
}
