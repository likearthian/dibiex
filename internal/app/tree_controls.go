package app

import "github.com/egoist/mygo/ui"

// Keep disclosure separate from the selectable row body.
func treeChevron(c *ui.Context, k palette, open bool, label string) ui.Element {
	button := ui.ButtonBase(c.Key("chevron:"+label)).Size(12, 22).Shrink(0).Center().Label(label).Tooltip(label)
	icon := "chevron-right"
	if open {
		icon = "chevron-down"
	}
	button.Children(func() { ui.Icon(c, controlIcon(icon)).Size(12, 12).TextColor(k.muted) })
	return button
}
