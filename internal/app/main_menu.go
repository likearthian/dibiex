package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) mainMenu(c *ui.Context, k palette) {
	button := toolbarAction(c, k, "menu", "Main menu", k.muted)
	if button.Clicked() {
		a.mainMenuOpen = !a.mainMenuOpen
	}
	ui.Popover(c.Key("main-menu-popup"), button, &a.mainMenuOpen, func() {
		ui.Column(c).Width(230).Padding(4).Gap(2).Children(func() {
			item := func(icon, label string, disabled bool, action func()) {
				row := ui.ButtonBase(c.Key(label)).FillWidth().Height(30).Padding(0, 10).Radius(4).Disabled(disabled).Role(ui.RoleMenuItem).Label(label)
				if row.Hovered() {
					row.Background(k.border)
				}
				row.Children(func() {
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, controlIcon(icon)).Size(16, 16).TextColor(k.muted)
						ui.Text(c, label).FontSize(12).TextColor(k.text)
					})
				})
				if row.Clicked() {
					a.mainMenuOpen = false
					action()
				}
			}
			item("plus", "New query tab", false, func() { a.addQuery("") })
			item("folder-open", "Open SQL file…", false, a.openSQLFile)
			item("save", "Save query", a.currentEditor() == nil || a.currentEditor().tableView != nil || a.ensureWorkspace().queryStoreBusy, func() { a.saveQueryDialog(a.currentEditor()) })
			item("execute-file", "Execute to File…", a.client == nil || a.currentEditor() == nil || a.currentEditor().tableView != nil || a.queryBusy(), a.openQueryExport)
			item("database", "Add connection…", a.connectionStoreBusy, a.open)
			ui.Divider(c)
			item("settings", "Settings…", a.win == nil, a.openResultSettings)
			item("table", "Services · Show results", false, func() { a.hideResults = false })
			item("folder-open", "Files", false, func() { a.toggleFilesPane(c) })
			item("inspector", "Inspector", false, a.toggleInspectorPane)
			ui.Divider(c)
			item("info", "About", a.win == nil, a.openAbout)
		})
	})
}

func (a *App) openAbout() {
	newModalWindow(mygo.WindowOptions{
		Title: "About dibiex", Width: 410, Height: 240, DisableResize: true, Parent: a.win,
	}, func(c *ui.Context) {
		k := paletteOf(c)
		ui.Column(c).Fill().Padding(24).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Image(c, titleBarIcon).Size(48, 48).Fit(ui.Contain)
			ui.Text(c, "dibiex").FontSize(22).FontWeight(600).TextColor(k.text)
			if a.version != "" {
				ui.Text(c, "Version "+a.version).FontSize(13).TextColor(k.muted)
			}
			ui.Text(c, "Native desktop SQL client").FontSize(13).TextColor(k.text)
		})
	})
}
