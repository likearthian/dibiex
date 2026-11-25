package app

import "github.com/egoist/mygo/ui"

func (a *App) savedQueryActions(c *ui.Context, k palette, row ui.Element, record savedQuery) bool {
	w := a.ensureWorkspace()
	button := ui.ButtonBase(c.Key("saved-query-actions:"+record.ID)).Size(24, 24).Shrink(0).Radius(4).Center().Label("Actions for saved query " + record.Name).Tooltip("Query actions")
	if row.Hovered() || button.Focused() || w.fileMenuOpen && w.fileMenuID == record.ID {
		if button.Hovered() {
			button.Background(k.selected)
		}
		button.Children(func() { ui.Icon(c, controlIcon("ellipsis")).Size(16, 16).TextColor(k.muted) })
	}
	clicked, right := button.Clicked(), row.RightClicked() || button.RightClicked()
	if clicked || right {
		if clicked && w.fileMenuID == record.ID {
			w.fileMenuOpen = !w.fileMenuOpen
		} else {
			w.fileMenuOpen = true
		}
		w.fileMenuID = record.ID
	}
	if w.fileMenuID == record.ID {
		ui.PopoverBase(c.Key("saved-query-menu:"+record.ID), row, &w.fileMenuOpen, func(panel ui.Element) {
			panel.Width(158).Margin(4, 0, 0, 0).Padding(4).Radius(6).Background(k.panel).Border(1, k.border).Label("Saved query menu")
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				item := func(icon, label string, action func()) {
					entry := ui.ButtonBase(c.Key(label)).FillWidth().Height(30).Padding(0, 10).Center().Radius(4).Role(ui.RoleMenuItem).Label(label).Disabled(w.queryStoreBusy)
					if entry.Hovered() {
						entry.Background(k.border)
					}
					entry.Children(func() {
						ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, controlIcon(icon)).Size(16, 16).TextColor(k.muted)
							ui.Text(c, label).FontSize(12).TextColor(k.text)
						})
					})
					if entry.Clicked() {
						w.fileMenuOpen = false
						action()
					}
				}
				item("rename", "Rename", func() { a.renameSavedQuery(record) })
				item("trash", "Delete", func() { a.removeSavedQuery(record) })
			})
		})
	}
	return clicked || right
}
