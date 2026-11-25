package app

import "github.com/egoist/mygo/ui"

func paneOutline(c *ui.Context) ui.Color {
	if c.Theme().Dark {
		return ui.Hex("#424449")
	}
	return ui.Hex("#dfe1e5")
}

type palette struct {
	bg, header, panel, editor, border, text, muted, danger, accent, selected, green ui.Color
}

func paletteOf(c *ui.Context) palette {
	if c.Theme().Dark {
		return palette{ui.Hex("#252629"), ui.Hex("#252629"), ui.Hex("#191a1c"), ui.Hex("#191a1c"), ui.Hex("#2e3033"), ui.Hex("#d5d7db"), ui.Hex("#92969f"), ui.Hex("#ef8585"), ui.Hex("#6a9bfa"), ui.Hex("#2c4774"), ui.Hex("#65aa75")}
	}
	return palette{ui.Hex("#f2f3f5"), ui.Hex("#e9ebef"), ui.Hex("#f7f8fa"), ui.Hex("#ffffff"), ui.Hex("#dfe1e5"), ui.Hex("#27282e"), ui.Hex("#737782"), ui.Hex("#c44242"), ui.Hex("#3574f0"), ui.Hex("#dce8fc"), ui.Hex("#388550")}
}
func sectionBar(c *ui.Context, k palette) ui.Element {
	return ui.Row(c).Height(36).Padding(0, 10).Gap(6).AlignItems(ui.Center).Background(k.panel).BorderWidth(0, 0, 1, 0).BorderColor(k.border)
}

func toolbarAction(c *ui.Context, k palette, glyph, label string, color ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Shrink(0).Radius(4).Center().Label(label).Tooltip(label)
	if b.Hovered() || b.Pressed() {
		b.Background(k.border)
	}
	b.Children(func() { ui.Icon(c, controlIcon(glyph)).Size(17, 17).TextColor(color) })
	return b
}

func toolbarDivider(c *ui.Context, k palette) {
	ui.Box(c).Size(1, 20).Margin(0, 4).Shrink(0).Background(k.border)
}
func toolButton(c *ui.Context, k palette, glyph, label string, active bool) ui.Element {
	b := ui.Box(c).Size(28, 28).Radius(4).Center().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
	color := k.muted
	if active {
		b.Background(k.selected)
		color = k.accent
	} else if b.Hovered() || b.Pressed() {
		b.Background(k.border)
		color = k.text
	}
	b.Children(func() { ui.Icon(c, controlIcon(glyph)).Size(16, 16).TextColor(color) })
	return b
}
func tabLabel(c *ui.Context, k palette, label string, active bool) ui.Element {
	b := ui.Row(c).Height(36).Padding(0, 14).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if active {
		b.Background(k.editor).BorderWidth(0, 0, 2, 0).BorderColor(k.accent)
	} else if b.Hovered() {
		b.Background(k.border)
	}
	b.Children(func() { ui.Text(c, label).FontSize(12).TextColor(k.text) })
	return b
}
