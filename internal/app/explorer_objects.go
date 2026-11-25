package app

import (
	"context"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) explorerEngine() database.Engine {
	if a.explorerCatalog.Engine != "" {
		return a.explorerCatalog.Engine
	}
	return a.client.Engine()
}
func (a *App) explorerOffset() float32 {
	if a.explorerEngine() == database.PostgreSQL {
		return 18
	}
	return 0
}

func (a *App) loadExplorerCatalog(force bool) {
	if a.client == nil || a.win == nil || a.client.Engine() != database.PostgreSQL || a.explorerLoading {
		return
	}
	a.explorerLoading = true
	a.explorerError = ""
	client, win, pipeline, version, parent := a.client, a.win, a.introspector, a.metadataVersion, a.connectionContext()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		var catalog database.ExplorerCatalog
		var err error
		if pipeline != nil {
			catalog, err = pipeline.Explorer(ctx, force)
		} else {
			catalog, err = client.ExplorerObjects(ctx)
		}
		warning := ""
		if pipeline != nil {
			warning = pipeline.Warning()
		}
		win.Update(func() {
			if a.closed || a.client != client || a.metadataVersion != version {
				return
			}
			a.explorerLoading = false
			a.cacheWarning = warning
			if err != nil {
				a.explorerError = err.Error()
				return
			}
			a.explorerCatalog = catalog
			a.refreshCatalogInspection()
			for _, group := range catalog.Groups {
				key := "extra:" + group.Scope + ":" + group.Name
				if _, ok := a.treeClosed[key]; !ok {
					a.treeClosed[key] = true
				}
			}
		})
	}()
}

func (a *App) extendedCatalogMenu(m *ui.Menu) {
	if m.Item("Refresh").Disabled(a.explorerLoading || a.client == nil).Chosen() {
		a.loadExplorerCatalog(true)
	}
	if m.Item("New SQL console").Chosen() {
		a.addQuery("")
	}
}

func (a *App) extraRoot(c *ui.Context, k palette, scope, title, icon string, depth int) {
	if a.treeNode(c, k, "extra-root:"+scope, title, icon, depth, -1, a.extendedCatalogMenu) {
		a.extraGroups(c, k, scope, depth+1)
	}
}

func (a *App) extraGroups(c *ui.Context, k palette, scope string, depth int) {
	names := []string{}
	switch {
	case strings.HasPrefix(scope, "schema:"):
		names = []string{"routines", "aggregates", "sequences", "object types"}
	case scope == "database":
		names = []string{"access methods", "casts", "extensions", "languages"}
	case scope == "server":
		names = []string{"roles", "tablespaces"}
	case scope == "virtual":
		names = []string{"virtual views"}
	}
	for _, name := range names {
		group := database.ExplorerGroup{Scope: scope, Name: name, State: "unloaded"}
		if !a.explorerCatalog.FetchedAt.IsZero() {
			group.State = "loaded"
		}
		for _, g := range a.explorerCatalog.Groups {
			if g.Name == name && (g.Scope == scope || g.Scope == "schema" && strings.HasPrefix(scope, "schema:")) {
				group = g
				break
			}
		}
		filter := strings.ToLower(a.filter)
		var items []database.Definition
		for _, d := range group.Items {
			if filter == "" || strings.Contains(strings.ToLower(name), filter) || strings.Contains(strings.ToLower(d.Name), filter) {
				items = append(items, d)
			}
		}
		if filter != "" && len(items) == 0 && !strings.Contains(name, filter) {
			continue
		}
		count := len(group.Items)
		if group.State == "unloaded" || group.State == "unavailable" {
			count = -1
		}
		key := "extra:" + scope + ":" + name
		if a.treeClosed == nil {
			a.treeClosed = map[string]bool{}
		}
		if _, ok := a.treeClosed[key]; !ok {
			a.treeClosed[key] = true
		}
		icon := "tree-folder"
		if scope == "virtual" {
			icon = "virtual-folder"
		}
		if !a.treeNode(c, k, key, name, icon, depth, count, a.extendedCatalogMenu) {
			continue
		}
		indent := float32(8 + (depth+1)*18)
		if group.State == "unloaded" {
			message := "Extended catalog not loaded"
			if a.explorerLoading {
				message = "Loading objects…"
			}
			if a.explorerError != "" {
				message = a.explorerError
			}
			ui.Text(c, message).Margin(4, 8, 4, indent).FontSize(11).TextColor(k.muted)
			if ui.Button(c.Key("load:"+key), "Load objects").Margin(0, 8, 4, indent).Disabled(a.explorerLoading || a.client == nil).Clicked() {
				a.loadExplorerCatalog(false)
			}
			continue
		}
		if group.Message != "" {
			ui.Text(c, group.Message).Margin(4, 8, 4, indent).FontSize(11).TextColor(k.danger)
		}
		for _, d := range items {
			leaf := name == "sequences" || scope == "database" || scope == "server"
			objectKey := key + ":" + d.Name
			row := ui.Row(c.Key(objectKey)).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, indent).Gap(6).AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Inspect " + name + " " + d.Name).Tooltip(d.Name)
			if a.selectedTree == objectKey {
				if leaf {
					row.Background(k.border).Radius(4)
				} else {
					row.Background(k.selected)
				}
			} else if row.Hovered() {
				row.Background(k.border)
			}
			menu := func(m *ui.Menu) {
				if leaf && m.Item("Inspect").Chosen() {
					a.inspectCatalogObject(scope, d, name)
				}
				if m.Item("Open SQL in editor").Disabled(d.SQL == "").Chosen() {
					a.addQuery(d.SQL)
				}
				if m.Item("Refresh").Disabled(a.client == nil || a.explorerLoading).Chosen() {
					a.loadExplorerCatalog(true)
				}
			}
			toggled := false
			row.Children(func() {
				if leaf {
					ui.Box(c).Width(12).Shrink(0)
				} else {
					if treeChevron(c, k, a.propertyOpen[objectKey], "Toggle object "+name+" "+d.Name).Clicked() {
						if a.propertyOpen == nil {
							a.propertyOpen = map[string]bool{}
						}
						a.propertyOpen[objectKey] = !a.propertyOpen[objectKey]
						toggled = true
					}
				}
				glyph := "catalog-object"
				color := k.accent
				if scope == "virtual" {
					glyph = "virtual-view"
					color = ui.Hex("#b185e8")
				}
				if name == "sequences" {
					glyph = "sequence"
					color = k.muted
				}
				ui.Icon(c, controlIcon(glyph)).Size(15, 15).TextColor(color)
				if name == "sequences" {
					ui.Text(c, d.Name).SingleLine().MinWidth(0).TextColor(k.text)
					ui.Text(c, sequenceDataType(d)).Label("Sequence type for " + d.Name).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted)
				} else {
					ui.Text(c, d.Name).SingleLine().Grow(1).MinWidth(0).TextColor(k.text)
				}
				objectActions(c, k, row, d.Name, menu)
			})
			if (row.Clicked() || row.Shortcut(0, ui.KeyEnter)) && !toggled {
				a.inspectCatalogObject(scope, d, name)
				a.selectedTree = objectKey
				a.setObjectPath(append(a.extraObjectPath(scope), name, d.Name)...)
			}
			if !leaf && a.propertyOpen[objectKey] {
				properties := d.Properties
				if d.SQL != "" {
					properties = append([]database.Property{{Name: "Definition", Value: d.SQL}}, properties...)
				}
				a.propertyTree(c, k, properties, "catalog:"+objectKey, 0, indent+18)
			}
		}
		if len(items) == 0 && group.State == "loaded" {
			ui.Text(c, "No objects").Margin(4, 8, 4, indent).FontSize(11).TextColor(k.muted)
		}
	}
}
