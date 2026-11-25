package app

import (
	"runtime"
	"time"

	"dibiex/assets"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var ownControls = runtime.GOOS == "windows"

var titleBarIcon = decodeBrandImage(assets.AppIcon)

func decodeBrandImage(data []byte) ui.ImageSource {
	icon, err := ui.DecodeImage(data)
	if err != nil {
		panic("decode brand image: " + err.Error())
	}
	return icon
}

var controlIcons = map[string]*ui.SVG{}

func controlIcon(name string) *ui.SVG {
	if s := controlIcons[name]; s != nil {
		return s
	}
	data, err := assets.ControlIcons.ReadFile("icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	s := ui.MustParseSVG(data)
	controlIcons[name] = s
	return s
}

func (a *App) titleBar(c *ui.Context, k palette) {
	titleBarRow(c, k).Children(func() {
		titleBarBrand(c)
		a.mainMenu(c, k)
		ui.Spacer(c).MinWidth(12)
		if toolButton(c, k, "database", "Toggle Database Explorer · Alt+1", !a.hideExplorer).Clicked() {
			a.hideExplorer = !a.hideExplorer
		}
		if toolButton(c, k, "table", "Toggle Services · Alt+8", !a.hideResults).Clicked() {
			a.hideResults = !a.hideResults
		}
		if toolButton(c, k, "folder-open", "Toggle Files", a.showFiles && !a.showInspector || a.filesDrawer).Clicked() {
			a.toggleFilesPane(c)
		}
		ui.Spacer(c).MinWidth(12)
		toolbarAction(c, k, "theme", "Appearance", k.muted).PopoverMenuButton(func(m *ui.Menu) {
			if m.Item("Dark theme").Chosen() {
				a.themeMode = "dark"
				if a.win != nil {
					mygo.Theme.SetSource(mygo.ThemeDark)
				}
			}
			if m.Item("Light theme").Chosen() {
				a.themeMode = "light"
				if a.win != nil {
					mygo.Theme.SetSource(mygo.ThemeLight)
				}
			}
		})
		if toolbarAction(c, k, "settings", "Settings", k.muted).Disabled(a.win == nil).Clicked() {
			a.openResultSettings()
		}
		a.windowControls(c, k)
	})
}

func titleBarRow(c *ui.Context, k palette) ui.Element {
	bar := c.TitleBar()
	left := float32(12)
	if bar.Left != 0 {
		left = bar.Left + 14
	}
	background := k.header
	if c.Vibrancy() {
		background = ui.Transparent
	}
	return ui.Row(c).FillWidth().Height(44).Shrink(0).Padding(0, max(bar.Right, 10), 0, left).Gap(8).AlignItems(ui.Center).Background(background).DragWindow()
}

func titleBarBrand(c *ui.Context) {
	ui.Image(c, titleBarIcon).Size(23, 23).Fit(ui.Contain).Shrink(0).Label("dibiex app icon")
}

func newModalWindow(opts mygo.WindowOptions, render func(*ui.Context)) *mygo.Window {
	opts.Modal = true
	opts.Frameless = true
	opts.BackgroundColor = "#252629"
	var win *mygo.Window
	opts.Content = ui.View(func(c *ui.Context) {
		k := paletteOf(c)
		ui.Column(c).Fill().Background(k.bg).Children(func() {
			titleBarRow(c, k).Children(func() {
				titleBarBrand(c)
				ui.Text(c, opts.Title).SingleLine().MinWidth(0).Grow(1).FontWeight(600).TextColor(k.text)
				if windowControl(c, k, "x", "Close", 17).Clicked() {
					win.Close()
				}
			})
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() { render(c) })
		})
	})
	win = mygo.NewWindow(opts)
	return win
}

func (a *App) windowControls(c *ui.Context, k palette) {
	win := a.win
	if !ownControls || win == nil || win.IsFullScreen() {
		return
	}
	maximized := win.IsMaximized()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		sep := ui.RGBA(0, 0, 0, .13)
		if c.Theme().Dark {
			sep = ui.RGBA(221, 209, 221, .13)
		}
		ui.Box(c).Size(1, 16).Margin(0, 6).Background(sep)
		if windowControl(c, k, "minus", "Minimize", 16).Clicked() {
			win.Minimize()
		}
		glyph, label := "square", "Maximize"
		if maximized {
			glyph, label = "copy", "Restore"
		}
		if windowControl(c, k, glyph, label, 13).Clicked() {
			if maximized {
				win.Unmaximize()
			} else {
				win.Maximize()
			}
		}
		if windowControl(c, k, "x", "Close", 17).Clicked() {
			win.Close()
		}
	})
}

func windowControl(c *ui.Context, k palette, glyph, label string, size float32) ui.Element {
	b := ui.Box(c).Size(30, 30).Center().Radius(30 / 2.6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
	col, hover, pressed := ui.Hex("#8a8a8f"), ui.RGBA(0, 0, 0, .055), ui.RGBA(0, 0, 0, .1)
	if c.Theme().Dark {
		col, hover, pressed = ui.Hex("#827d83"), ui.RGBA(231, 220, 228, .08), ui.RGBA(231, 220, 228, .14)
	}
	if glyph == "x" {
		hover, pressed = ui.Hex("#c42b1c"), ui.Hex("#c42b1c").Alpha(.8)
		if b.Hovered() || b.Pressed() {
			col = ui.Hex("#ffffff")
		}
	} else if b.Hovered() {
		col = k.text
	}
	if b.Pressed() {
		b.Background(pressed)
	} else if b.Hovered() {
		b.Background(hover)
	}
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	b.Children(func() {
		ui.Icon(c, controlIcon(glyph)).Size(size, size).TextColor(col)
	})
	return b
}
