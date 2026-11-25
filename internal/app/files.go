package app

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (a *App) editorTab(c *ui.Context, k palette, t *queryTab, active bool) ui.Element {
	row := ui.Row(c.Key(fmt.Sprintf("query-tab:%p:%d", a, t.id))).Height(30).Padding(0, 9).Gap(6).Radius(5).MinWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(a.consoleLabel(t.name)).Tooltip(t.name + " · " + a.workspaceName())
	if active {
		row.Background(k.header).Border(1, k.border)
	} else if row.Hovered() {
		row.Background(k.border)
	}
	closed := false
	row.Children(func() {
		if t.tableView != nil {
			ui.Icon(c, controlIcon("table")).Size(15, 15).TextColor(k.muted)
		} else if a.hasConnection() {
			databaseIcon(c, k, a.client.Engine())
		} else if a.lastConnection != nil {
			databaseIcon(c, k, a.lastConnection.Config.Engine)
		} else {
			ui.Icon(c, controlIcon("console")).Size(15, 15).TextColor(k.muted)
		}
		ui.Text(c, t.name).SingleLine().FontSize(12).TextColor(k.text)
		ui.Text(c, a.workspaceName()).SingleLine().MaxWidth(150).FontSize(11).TextColor(k.muted)
		if t.execution.running {
			ui.Text(c, "Running…").FontSize(10).TextColor(k.accent)
		}
		if !a.hasConnection() && a.lastConnection != nil {
			ui.Text(c, "Disconnected").FontSize(10).TextColor(k.muted)
		}
		if row.Hovered() {
			if toolbarAction(c, k, "x", "Close tab "+a.consoleLabel(t.name), k.muted).Size(18, 18).Clicked() {
				a.closeTab(t)
				closed = true
			}
		} else {
			ui.Box(c).Size(18, 18)
		}
	})
	if closed {
		return ui.Element{}
	}
	return row
}

// Files lists the real in-memory query consoles; no filesystem scan runs in a view.
func (a *App) filesPane(c *ui.Context, k palette) {
	ui.Column(c).Fill().MinWidth(0).Radius(10).Border(1, paneOutline(c)).Clip().Background(k.panel).Label("Right sidebar content").Children(func() {
		a.rightSidebarHeader(c, k, false)
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Padding(6, 0).Children(func() {
				ui.Row(c).Height(28).Padding(0, 12).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, controlIcon("folder-open")).Size(15, 15).TextColor(k.muted)
					ui.Text(c, "dibiex").TextColor(k.text)
					ui.Text(c, "workspace").FontSize(11).TextColor(k.muted)
				})
				row := ui.Row(c).Height(28).FillWidth().Padding(0, 10).Gap(6).AlignItems(ui.Center).Label("Scratches and Consoles").Tooltip("Open SQL consoles across all connections")
				if row.Hovered() {
					row.Background(k.border)
				}
				row.Children(func() {
					if treeChevron(c, k, !a.filesCollapsed, "Toggle Scratches and Consoles").Clicked() {
						a.filesCollapsed = !a.filesCollapsed
					}
					ui.Icon(c, controlIcon("console")).Size(15, 15).TextColor(k.muted)
					ui.Text(c, "Scratches and Consoles").SingleLine().TextColor(k.text)
				})
				if !a.filesCollapsed {
					for sessionID, owner := range a.connectionSessions() {
						if len(owner.tabs) == 0 && len(owner.queryRecords()) == 0 && !owner.hasConnection() && owner.lastConnection == nil {
							continue
						}
						ui.Column(c.Key(fmt.Sprintf("console-workspace:%d", sessionID))).FillWidth().MinWidth(0).Children(func() {
							group := ui.Row(c).FillWidth().Height(28).Padding(0, 10, 0, 26).Gap(6).AlignItems(ui.Center).Label("Console folder " + owner.workspaceName())
							if group.Hovered() {
								group.Background(k.border)
							}
							group.Children(func() {
								if treeChevron(c, k, !owner.filesGroupClosed, "Toggle consoles for "+owner.workspaceName()).Clicked() {
									owner.filesGroupClosed = !owner.filesGroupClosed
								}
								ui.Icon(c, controlIcon("folder-open")).Size(15, 15).TextColor(k.accent)
								ui.Text(c, owner.workspaceName()).SingleLine().MinWidth(0).TextColor(k.text)
								count := len(owner.queryRecords())
								for _, tab := range owner.tabs {
									if tab.savedID == "" && tab.tableView == nil {
										count++
									}
								}
								ui.Text(c, fmt.Sprint(count)).FontSize(11).TextColor(k.muted)
							})
							if !owner.filesGroupClosed {
								for _, record := range owner.queryRecords() {
									item := ui.Row(c.Key("saved-query:"+record.ID)).FillWidth().MinWidth(0).Height(28).Padding(0, 10, 0, 62).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Open saved query " + owner.consoleLabel(record.Name))
									if owner == a && owner.currentEditor() != nil && owner.currentEditor().savedID == record.ID {
										item.Background(k.selected)
									} else if item.Hovered() {
										item.Background(k.border)
									}
									actionsClicked := false
									item.Children(func() {
										ui.Icon(c, controlIcon("sql-file")).Size(15, 15).TextColor(k.accent)
										ui.Text(c, record.Name).SingleLine().Grow(1).MinWidth(0).TextColor(k.text)
										actionsClicked = owner.savedQueryActions(c, k, item, record)
									})
									if item.Clicked() && !actionsClicked {
										owner.openSavedQuery(record)
									}
								}
								for i, t := range owner.tabs {
									if t.savedID != "" || t.tableView != nil {
										continue
									}
									item := ui.Row(c.Key(t.id)).FillWidth().MinWidth(0).Height(28).Padding(0, 10, 0, 62).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Open console " + owner.consoleLabel(t.name)).Tooltip(owner.workspaceName() + " / " + t.name)
									if owner == a && i == a.activeTab {
										item.Background(k.selected)
									} else if item.Hovered() {
										item.Background(k.border)
									}
									item.Children(func() {
										ui.Icon(c, controlIcon("sql-file")).Size(15, 15).TextColor(k.accent)
										ui.Text(c, t.name).SingleLine().MinWidth(0).TextColor(k.text)
									})
									if item.Clicked() {
										owner.selectQueryTab(i)
									}
								}
							}
						})
					}
				}
			})
		})
	})
}
