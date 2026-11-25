package app

import (
	"fmt"
	"strings"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) rightSidebarHeader(c *ui.Context, k palette, inspector bool) {
	ui.Row(c).Height(42).Padding(0, 6).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Padding(3).Gap(3).Radius(6).Background(k.header).Children(func() {
			for _, tab := range []struct {
				title, icon string
				active      bool
			}{{"Files", "folder-open", !inspector}, {"Inspector", "inspector", inspector}} {
				button := ui.ButtonBase(c.Key("right-tab:"+tab.title)).Height(28).Grow(1).MinWidth(0).Padding(0, 6).Gap(5).Radius(4).AlignItems(ui.Center).Label("Show " + tab.title).Tooltip("Show " + tab.title)
				color := k.muted
				if tab.active {
					button.Background(k.panel)
					color = k.text
				} else if button.Hovered() {
					button.Background(k.border)
				}
				button.Children(func() {
					ui.Icon(c, controlIcon(tab.icon)).Size(13, 13).Shrink(0).TextColor(color)
					ui.Text(c, tab.title).SingleLine().MinWidth(0).FontSize(11).FontWeight(500).TextColor(color)
				})
				if button.Clicked() {
					a.selectRightPane(c, tab.title == "Inspector")
				}
			}
		})
		title := "Files"
		if inspector {
			title = "Inspector"
		}
		if toolButton(c, k, "minus", "Hide "+title, false).Size(24, 24).Clicked() {
			a.showInspector, a.showFiles, a.filesDrawer = false, false, false
		}
	})
}

func inspectorHeading(c *ui.Context, k palette, name, kind, context, icon, label string, actions func()) {
	ui.Column(c).FillWidth().MinWidth(0).Padding(14, 14, 12, 14).Gap(9).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(9).AlignItems(ui.Center).Children(func() {
			iconColor := k.accent
			if kind == "Column" {
				iconColor = k.muted
			}
			ui.Box(c).Size(32, 32).Shrink(0).Radius(7).Background(k.header).Center().Children(func() { ui.Icon(c, controlIcon(icon)).Size(19, 19).TextColor(iconColor) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, kind).FontSize(10).FontWeight(500).TextColor(k.accent)
				ui.Text(c, name).Label(label).SingleLine().MinWidth(0).FontSize(16).FontWeight(600).TextColor(k.text).Tooltip(name)
			})
			if actions != nil {
				actions()
			}
		})
		if context != "" {
			ui.Text(c, context).SingleLine().MinWidth(0).FontSize(11).TextColor(k.muted).Tooltip(context)
		}
	})
}

func inspectorSection(c *ui.Context, k palette, title string) {
	ui.Text(c, title).Margin(6, 0, 1, 0).FontSize(12).FontWeight(600).TextColor(k.text)
}

func inspectorField(c *ui.Context, k palette, label, value string, nativeName ...string) {
	tip := label
	if len(nativeName) > 0 {
		tip = nativeName[0]
	}
	ui.Row(c).FillWidth().MinWidth(0).Gap(8).Padding(5, 0).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.border.Alpha(.5)).Label(label + ": " + value).Children(func() {
		ui.Text(c, label).Width(88).Shrink(0).FontSize(11).TextColor(k.muted).Tooltip(tip)
		ui.Row(c).Grow(1).MinWidth(0).MinHeight(28).Padding(5, 8).Gap(5).Radius(4).Background(k.header).AlignItems(ui.Center).Children(func() {
			if value == "true" || value == "Yes" || value == "YES" {
				ui.Box(c).Size(5, 5).Shrink(0).Radius(3).Background(k.green)
			}
			ui.Text(c, value).SingleLine().Grow(1).MinWidth(0).Selectable().FontSize(12).FontWeight(500).TextColor(k.text).Tooltip(value)
		})
	})
}

func inspectorCode(c *ui.Context, k palette, title, sql string) {
	ui.Column(c).FillWidth().MinWidth(0).Radius(6).Background(k.header).Border(1, k.border).Children(func() {
		ui.Row(c).Height(30).Padding(0, 8).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
			ui.Icon(c, controlIcon("sql-file")).Size(13, 13).TextColor(k.muted)
			ui.Text(c, title).Grow(1).FontSize(11).FontWeight(500).TextColor(k.muted)
			if toolbarAction(c, k, "copy", "Copy "+title, k.muted).Size(22, 22).Clicked() {
				c.WriteClipboard(sql)
			}
		})
		ui.Text(c, sql).Margin(10).Selectable().Font("JetBrains Mono").FontSize(11).TextColor(k.text)
	})
}

func inspectorPropertyLabel(name string) string {
	labels := map[string]string{"rolname": "Name", "rolsuper": "Superuser", "rolinherit": "Inherit privileges", "rolcanlogin": "Can log in", "rolcreatedb": "Create databases", "rolcreaterole": "Create roles", "rolreplication": "Replication", "rolbypassrls": "Bypass row security", "rolconnlimit": "Connection limit", "rolvaliduntil": "Valid until", "amname": "Name", "amtype": "Type", "amhandler": "Handler ID", "extname": "Name", "extversion": "Version", "extowner": "Owner ID", "lanname": "Name", "lanpltrusted": "Trusted", "spcname": "Name", "spcowner": "Owner ID"}
	if label, ok := labels[name]; ok {
		return label
	}
	return name
}

func (a *App) inspectorPropertiesTree(c *ui.Context, k palette, properties []database.Property, scope string) {
	if a.propertyOpen == nil {
		a.propertyOpen = map[string]bool{}
	}
	for i, p := range properties {
		key := fmt.Sprintf("%s\x00%d:%s", scope, i, p.Name)
		if len(p.Children) == 0 {
			inspectorField(c, k, inspectorPropertyLabel(p.Name), p.Value, p.Name)
			continue
		}
		ui.Column(c).FillWidth().MinWidth(0).Margin(3, 0).Radius(5).Border(1, k.border).Children(func() {
			row := ui.Row(c.Key(key)).FillWidth().Height(32).Padding(0, 8).Gap(6).AlignItems(ui.Center).Label("Property " + key).Tooltip(p.Name)
			if row.Hovered() {
				row.Background(k.header)
			}
			row.Children(func() {
				if treeChevron(c, k, a.propertyOpen[key], "Toggle property "+key).Clicked() {
					a.propertyOpen[key] = !a.propertyOpen[key]
				}
				ui.Text(c, inspectorPropertyLabel(p.Name)).SingleLine().Grow(1).MinWidth(0).FontSize(11).FontWeight(500).TextColor(k.text)
				ui.Text(c, fmt.Sprint(len(p.Children))).FontSize(10).TextColor(k.muted)
			})
			if a.propertyOpen[key] {
				ui.Column(c).FillWidth().MinWidth(0).Padding(0, 8, 8, 8).Children(func() { a.inspectorPropertiesTree(c, k, p.Children, key) })
			}
		})
	}
}

func inspectorContext(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}
