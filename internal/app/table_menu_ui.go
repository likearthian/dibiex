package app

import (
	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type tableMenuState struct {
	open bool
	id   string
	path []int
}

func (a *App) tableActions(c *ui.Context, k palette, row ui.Element, table database.Table) bool {
	if a.client.Engine() != database.PostgreSQL || table.Kind != "" && table.Kind != "Tables" && table.Kind != "Partitioned tables" {
		objectActions(c, k, row, table.Label(), func(m *ui.Menu) { a.tableMenu(m, table) })
		return false
	}
	return a.postgresTableActions(c, k, row, table)
}

func (a *App) postgresTableActions(c *ui.Context, k palette, row ui.Element, table database.Table) bool {
	state := &a.tableMenuState
	button := ui.ButtonBase(c.Key("object-actions:"+table.ID())).Size(24, 24).Shrink(0).Radius(4).Center().Label("Actions for " + table.Label()).Tooltip("Table actions")
	if row.Hovered() || button.Focused() || state.open && state.id == table.ID() {
		if button.Hovered() {
			button.Background(k.selected)
		}
		button.Children(func() { ui.Icon(c, controlIcon("ellipsis")).Size(16, 16).TextColor(k.muted) })
	}
	clicked, right := button.Clicked(), row.RightClicked() || button.RightClicked()
	right = right || row.Shortcut(ui.Shift, ui.KeyF10) || button.Shortcut(ui.Shift, ui.KeyF10)
	if clicked || right {
		if clicked && state.id == table.ID() {
			state.open = !state.open
		} else {
			state.open = true
		}
		state.id, state.path = table.ID(), nil
	}
	if state.id == table.ID() {
		ui.PopoverBase(c.Key("table-menu:"+table.ID()), row, &state.open, func(panel ui.Element) {
			a.drawTableMenu(c, k, panel, a.postgresTableItems(table))
		})
	}
	return clicked || right
}

func (a *App) drawTableMenu(c *ui.Context, k palette, panel ui.Element, items []tableMenuItem) {
	state := &a.tableMenuState
	columns := [][]tableMenuItem{items}
	tops := []float32{0}
	_, height := c.Size()
	available := height - 32
	menuHeight := func(entries []tableMenuItem) float32 {
		h := float32(0)
		for _, item := range entries {
			if item.label == "" {
				h += 8
			} else {
				h += 29
			}
		}
		return h
	}
	for _, index := range state.path {
		if index < 0 || index >= len(items) || len(items[index].children) == 0 {
			break
		}
		top := tops[len(tops)-1] + menuHeight(items[:index])
		items = items[index].children
		tops = append(tops, max(float32(0), min(top, available-menuHeight(items)-10)))
		columns = append(columns, items)
	}
	widths := []float32{244, 310, 200}
	width := float32(4 * (len(columns) - 1))
	for level := range columns {
		width += widths[level]
	}
	panel.Width(width).Label("Table context menu")
	ui.Row(c).Gap(4).AlignItems(ui.Start).Children(func() {
		for level, entries := range columns {
			ui.Scroll(c.Key(level)).Width(widths[level]).Height(min(menuHeight(entries)+10, available-tops[level])).Margin(tops[level], 0, 0, 0).Padding(4).Radius(6).Background(k.panel).Border(1, k.border).Children(func() {
				ui.Column(c).FillWidth().Gap(1).Children(func() {
					for index, item := range entries {
						if item.label == "" {
							ui.Divider(c).Margin(3, 0)
							continue
						}
						row := ui.ButtonBase(c.Key(item.label)).FillWidth().Height(28).Padding(0, 9).Radius(4).Role(ui.RoleMenuItem).Label(item.label).Disabled(item.disabled)
						pressed := row.Clicked()
						expand := len(item.children) > 0 && row.Shortcut(0, ui.KeyRight)
						selected := level < len(state.path) && state.path[level] == index
						if !item.disabled && (row.Hovered() || row.Focused() || selected) {
							row.Background(k.border)
						}
						if !item.disabled && (row.Hovered() || pressed || expand) {
							state.path = append(state.path[:min(level, len(state.path))], index)
							if len(item.children) == 0 {
								state.path = state.path[:level]
							}
						}
						color := k.text
						if item.disabled {
							color = k.muted
							if item.label != "Refresh…" {
								row.Tooltip("Temporarily disabled: requires pg_dump or pg_restore.")
							}
						}
						row.Children(func() {
							ui.Row(c).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, controlIcon(item.icon)).Size(15, 15).Shrink(0).TextColor(k.muted).Label("Icon for " + item.label)
								ui.Text(c, item.label).SingleLine().Grow(1).MinWidth(0).FontSize(12).TextColor(color)
								if len(item.children) > 0 {
									ui.Icon(c, controlIcon("chevron-right")).Size(12, 12).TextColor(k.muted)
								}
							})
						})
						if pressed && !item.disabled && len(item.children) == 0 && item.action != nil {
							state.open = false
							item.action()
						}
					}
				})
			})
		}
	})
}
