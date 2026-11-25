package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) applyCatalog(schemas []database.Schema, reset bool) {
	if reset || a.treeClosed == nil {
		a.treeClosed = map[string]bool{}
	}
	if reset {
		a.explorerSearch = explorerSchemaSearch{}
	}
	a.schemaErrors = map[string]string{}
	for _, s := range schemas {
		if a.client.Engine() == database.PostgreSQL {
			for _, group := range []string{"Tables", "Partitioned tables", "Views", "Materialized views", "Foreign tables"} {
				key := "group:" + s.ID() + ":" + group
				if _, ok := a.treeClosed[key]; !ok {
					a.treeClosed[key] = true
				}
			}
		}
		for _, t := range s.Tables {
			key := "table:" + t.ID()
			if _, ok := a.treeClosed[key]; !ok {
				a.treeClosed[key] = true
			}
		}
		if _, known := a.treeClosed["schema:"+s.ID()]; !s.Loaded && !known {
			a.treeClosed["schema:"+s.ID()] = true
		}
	}
	a.schemas = schemas
	if selected := a.inspectorDefinition; selected != nil {
		found := false
		for _, s := range schemas {
			for _, t := range s.Tables {
				if t.ID() == selected.Table.ID() {
					found = true
					break
				}
			}
		}
		if !found {
			a.inspectorDefinition = nil
			a.activeTable = ""
			a.selectedTree = ""
			a.objectPath = nil
		}
	}
	if selected := a.inspectorColumn; selected != nil {
		found := false
		for _, s := range schemas {
			for _, t := range s.Tables {
				if t.ID() == selected.Table.ID() {
					found = true
					break
				}
			}
		}
		if !found {
			a.inspectorColumn = nil
			a.activeTable = ""
			a.selectedTree = ""
			a.objectPath = nil
		}
	}
}
func (a *App) loadSchema(index int, force ...bool) {
	if a.closed || a.client == nil || a.win == nil {
		return
	}
	schema, client, win := a.schemas[index], a.client, a.win
	id, version := schema.ID(), a.metadataVersion
	if a.schemaIsLoading(id) {
		return
	}
	if a.schemaLoading == nil {
		a.schemaLoading = make(map[string]*uint64)
	}
	a.schemaLoading[id] = &version
	if a.schemaErrors == nil {
		a.schemaErrors = make(map[string]string)
	}
	delete(a.schemaErrors, id)
	runner, pipeline := a.catalogRunner()
	tab := a.currentEditor()
	refresh := len(force) > 0 && force[0]
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	go func() {
		defer cancel()
		var tables []database.Table
		var err error
		warning := ""
		if pipeline != nil {
			tables, err = pipeline.Schema(ctx, schema, refresh)
			warning = pipeline.Warning()
		} else {
			tables, err = runner.SchemaTables(ctx, schema)
		}
		win.Update(func() {
			if a.closed || a.client != client || a.schemaLoading[id] != &version {
				return
			}
			delete(a.schemaLoading, id)
			if a.metadataVersion != version || client.Engine() == database.SQLite && a.currentEditor() != tab {
				return
			}
			a.cacheWarning = warning
			for i := range a.schemas {
				if a.schemas[i].ID() != id {
					continue
				}
				if err != nil {
					a.schemaErrors[id] = err.Error()
					return
				}
				a.schemas[i].Tables, a.schemas[i].Loaded = tables, true
				a.schemas[i].FetchedAt = time.Now().UTC()
				a.schemas[i].FromCache = false
				for _, t := range tables {
					key := "table:" + t.ID()
					if _, known := a.treeClosed[key]; !known {
						a.treeClosed[key] = true
					}
				}
				return
			}
		})
	}()
}

func (a *App) schemaIsLoading(id string) bool {
	version := a.schemaLoading[id]
	return version != nil && *version == a.metadataVersion
}

func (a *App) treeNode(c *ui.Context, k palette, key, label, glyph string, depth int, count int, menu ...func(*ui.Menu)) bool {
	closed, known := a.treeClosed[key]
	if !known && a.sessionContext != nil {
		closed = true
		a.treeClosed[key] = true
	}
	displayLabel := label
	if strings.HasPrefix(key, "group:") {
		displayLabel = strings.ToLower(label)
	}
	if a.filter != "" || a.scopedSearchOpens(key) {
		closed = false
	}
	row := ui.Row(c.Key(key)).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, 8+float32(depth*18)).Gap(6).AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Select " + label).Tooltip(label)
	if a.selectedTree == key {
		row.Background(k.border).Radius(4)
	} else if row.Hovered() {
		row.Background(k.border)
	}
	toggled := false
	row.Children(func() {
		if treeChevron(c, k, !closed, "Toggle "+label).Clicked() {
			a.activateConnection()
			a.treeClosed[key] = !a.treeClosed[key]
			a.catalogPicker.open = false
			toggled = true
		}
		if key == "connection" {
			databaseIcon(c, k, a.explorerEngine())
		} else {
			color := k.muted
			if strings.HasPrefix(key, "group:") {
				color = k.accent
				glyph = "tree-folder"
			} else if strings.HasPrefix(key, "schema:") {
				glyph = "schema"
			}
			if strings.HasPrefix(key, "extra:") {
				color = k.accent
			}
			if glyph == "database-node" {
				color = k.accent
			}
			if glyph == "virtual-folder" {
				color = ui.Hex("#b185e8")
			}
			ui.Icon(c, controlIcon(glyph)).Size(15, 15).TextColor(color)
		}
		if count >= 0 {
			ui.Row(c).MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Text(c, displayLabel).Label(label).SingleLine().MinWidth(0).TextColor(k.text)
				ui.Text(c, fmt.Sprint(count)).Label("Object count for " + label).Shrink(0).FontSize(11).TextColor(k.muted)
			})
			ui.Spacer(c).MinWidth(0)
		} else if kind, project := a.catalogPickerScope(key); kind != "" {
			ui.Text(c, label).SingleLine().MinWidth(0).TextColor(k.text)
			a.catalogPickerButton(c, k, key, label, kind, project)
			ui.Spacer(c).MinWidth(0)
		} else {
			ui.Text(c, label).SingleLine().Grow(1).MinWidth(0).TextColor(k.text)
		}
		if len(menu) > 0 {
			objectActions(c, k, row, label, menu[0])
		}
	})
	if (row.Clicked() || row.Shortcut(0, ui.KeyEnter)) && !toggled {
		a.activateConnection()
		a.selectedTree = key
		a.selectTreePath(key, label)
	}
	return !closed
}
func tableGroup(table database.Table) string {
	if table.Kind == "" {
		return "Tables"
	}
	return table.Kind
}
func (a *App) sidebar(c *ui.Context, k palette) {
	if a.treeClosed == nil {
		a.treeClosed = map[string]bool{}
	}
	if a.schemaErrors == nil {
		a.schemaErrors = map[string]string{}
	}
	ui.Column(c).Fill().MinWidth(0).Radius(10).Border(1, paneOutline(c)).Clip().Background(k.panel).Children(func() {
		sectionBar(c, k).Children(func() {
			ui.Text(c, "Database Explorer").SingleLine().MinWidth(0).Grow(1).FontWeight(600).TextColor(k.text)
			if toolButton(c, k, "minus", "Hide Database Explorer · Alt+1", false).Clicked() {
				a.hideExplorer = true
			}
		})
		ui.Row(c).Height(34).Padding(0, 6).Gap(2).AlignItems(ui.Center).Children(func() {
			if toolButton(c, k, "plus", "Add connection", false).Disabled(a.connectionStoreBusy).Clicked() {
				a.open()
			}
			if toolbarAction(c, k, "refresh", "Refresh database objects", k.muted).Disabled(a.client == nil || a.busy).Clicked() {
				a.refreshCatalog()
			}
			toolbarDivider(c, k)
			if toolbarAction(c, k, "console", "New SQL console", k.muted).Clicked() {
				a.addQuery("")
			}
			if toolbarAction(c, k, "collapse", "Collapse database objects", k.muted).Clicked() {
				for key := range a.treeClosed {
					if key != "connection" {
						a.treeClosed[key] = true
					}
				}
			}
			if toolButton(c, k, "search", "Search database objects", !a.hideSearch).Clicked() {
				a.hideSearch = !a.hideSearch
				if a.hideSearch {
					a.filter = ""
				}
			}
			ui.Spacer(c)
			if toolButton(c, k, "inspector", "Inspector", a.showInspector && !a.filesDrawer).Clicked() {
				a.toggleInspectorPane()
			}
		})
		if !a.hideSearch {
			ui.TextInput(c.Key("table-filter"), &a.filter).Margin(0, 8, 8, 8).Placeholder("Search database objects").Label("Search tables and views").Tooltip("Search visible schemas, datasets, and their database objects")
			a.discoverExplorerSearch(c, k)
		}
		ui.Scroll(c).FillWidth().MinWidth(0).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Padding(4, 0).Children(func() {
				a.connectionRoots(c, k)
			})
		})
		if a.cacheWarning != "" {
			ui.Text(c, a.cacheWarning).Margin(4, 8).MaxHeight(48).FontSize(10).TextColor(k.danger)
		}
	})
}

func (a *App) activeConnectionTree(c *ui.Context, k palette) {
	if !a.treeNode(c, k, "connection", a.connectionLabel(), "database", 0, -1, a.connectionMenu) {
		return
	}
	if a.explorerEngine() == database.PostgreSQL {
		name := a.explorerCatalog.Database
		if name == "" {
			name = a.client.Scope()
		}
		if a.treeNode(c, k, "database:"+name, name, "database-node", 1, -1, a.extendedCatalogMenu) {
			a.schemaTrees(c, k, 1)
			a.extraRoot(c, k, "database", "Database Objects", "database-folder", 2)
			a.extraGroups(c, k, "virtual", 2)
		}
		a.extraRoot(c, k, "server", "Server Objects", "server-folder", 1)
		return
	}
	if a.explorerEngine() == database.BigQuery {
		hidden := a.explorerVisibility().ExplorerHiddenProjects
		for _, project := range a.bigQueryProjects() {
			if hidden[project] {
				continue
			}
			if a.treeNode(c, k, "project:"+project, project, "database-node", 1, -1, func(m *ui.Menu) { a.projectMenu(m, project) }) {
				a.schemaTrees(c, k, 1, project)
			}
		}
		return
	}
	if a.treeNode(c, k, "catalog-databases", "Databases", "database-node", 1, -1) {
		a.schemaTrees(c, k, 1)
	}
}

func (a *App) bigQueryProject() string {
	if a.client != nil {
		settings := a.client.Settings()
		if settings.DatasetProject != "" {
			return settings.DatasetProject
		}
		return settings.Project
	}
	if len(a.schemas) > 0 {
		return a.schemas[0].Project
	}
	return ""
}

func (a *App) projectMenu(m *ui.Menu, project string) {
	if m.Item("Remove project from tree").Disabled(a.busy || a.connectionStoreBusy || a.client == nil).Chosen() {
		a.removeBigQueryProject(project)
	}
	if m.Item("Add project…").Disabled(a.busy || a.connectionStoreBusy || a.client == nil).Chosen() {
		a.openBigQueryProject()
	}
	if m.Item("Refresh").Disabled(a.busy || a.client == nil).Chosen() {
		a.refreshBigQueryProject(project)
	}
}

func (a *App) schemaTrees(c *ui.Context, k palette, shift int, projects ...string) {
	shown := 0
	hidden := a.explorerVisibility().ExplorerHiddenSchemas
	for index, schema := range a.schemas {
		if hidden[schema.ID()] {
			continue
		}
		if len(projects) > 0 && schema.Project != projects[0] {
			continue
		}
		filter := strings.ToLower(a.filter)
		scoped := a.explorerSearch.schemaID == schema.ID()
		scopedQuery := strings.ToLower(a.explorerSearch.query)
		schemaMatch := strings.Contains(strings.ToLower(schema.Name), filter)
		if a.explorerEngine() == database.BigQuery && strings.Contains(strings.ToLower(schema.Project), filter) {
			schemaMatch = true
		}
		groups := make(map[string][]database.Table)
		counts := make(map[string]int)
		tableCount := 0
		for _, table := range schema.Tables {
			if scoped && !strings.Contains(strings.ToLower(table.Name), scopedQuery) {
				continue
			}
			if schemaMatch || strings.Contains(strings.ToLower(table.Name), filter) || columnsMatch(a.metadata[table.ID()].Columns, filter) {
				group := tableGroup(table)
				counts[group]++
				if len(groups[group]) < explorerObjectLimit {
					groups[group] = append(groups[group], table)
				}
				tableCount++
			}
		}
		extraMatch := false
		for _, group := range a.explorerCatalog.Groups {
			if group.Scope == "schema:"+schema.Name {
				for _, d := range group.Items {
					if strings.Contains(strings.ToLower(d.Name), filter) || strings.Contains(group.Name, filter) {
						extraMatch = true
						break
					}
				}
			}
		}
		if filter != "" && !schemaMatch && tableCount == 0 && !extraMatch {
			continue
		}
		shown++
		key := "schema:" + schema.ID()
		wasClosed := a.treeClosed[key]
		open := a.treeNode(c, k, key, schema.Name, "folder-open", 1+shift, -1, func(m *ui.Menu) { a.schemaMenu(m, schema, index) })
		expanded := wasClosed && !a.treeClosed[key]
		if !schema.Loaded && (expanded || (!a.treeClosed[key] || scoped) && a.schemaErrors[schema.ID()] == "") {
			a.loadSchema(index)
		}
		if !open {
			continue
		}
		if scoped {
			a.schemaSearchInput(c, k, schema, shift)
		}
		if message := a.schemaErrors[schema.ID()]; message != "" {
			ui.Text(c, message).Margin(4, 12, 4, 36).MaxHeight(60).FontSize(11).TextColor(k.danger)
			if ui.Button(c.Key("retry:"+schema.ID()), "Retry loading").Margin(0, 0, 0, 36).Clicked() {
				a.loadSchema(index, schema.Loaded)
			}
		} else if a.schemaIsLoading(schema.ID()) {
			ui.Text(c, "Loading…").Margin(4, 0, 4, 36).FontSize(11).TextColor(k.muted)
		}
		if !schema.Loaded {
			continue
		}
		flat := a.explorerEngine() == database.BigQuery
		remaining := explorerObjectLimit
		for _, group := range []string{"Tables", "Partitioned tables", "Views", "Materialized views", "Foreign tables", "External tables", "Snapshots", "Clones"} {
			objects := groups[group]
			if len(objects) == 0 && !((a.client.Engine() == database.PostgreSQL || a.client.Engine() == database.SQLite) && group == "Tables") {
				continue
			}
			var menus []func(*ui.Menu)
			if (a.client.Engine() == database.PostgreSQL || a.client.Engine() == database.SQLite) && (group == "Tables" || group == "Partitioned tables") {
				menus = append(menus, func(m *ui.Menu) {
					if m.Item("Create table…").Disabled(a.busy).Chosen() {
						a.openTableDesigner(schema.Name)
					}
					if m.Item("Refresh").Disabled(a.busy || a.schemaIsLoading(schema.ID())).Chosen() {
						a.invalidateMetadata()
						a.loadSchema(index, true)
					}
				})
			}
			if a.explorerEngine() != database.BigQuery && !a.treeNode(c, k, "group:"+schema.ID()+":"+group, group, objectIcon(group), 2+shift, counts[group], menus...) {
				continue
			}
			tableShift := shift
			if a.explorerEngine() == database.BigQuery {
				tableShift--
			}
			limit := explorerObjectLimit
			if flat {
				limit = remaining
			}
			visible := min(len(objects), limit)
			remaining -= visible
			for _, table := range objects[:visible] {
				row := ui.Row(c.Key(table.ID())).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, min(float32(68+tableShift*18), max(float32(8), a.explorerWidth-100))).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Inspect table " + table.Label()).Tooltip(table.Label())
				if a.selectedTree == "table:"+table.ID() || a.selectedTree == "" && a.activeTable == table.ID() {
					row.Background(k.selected)
				} else if row.Hovered() {
					row.Background(k.border)
				}
				toggled, menuClicked := false, false
				row.Children(func() {
					if treeChevron(c, k, !a.treeClosed["table:"+table.ID()], "Toggle table "+table.Label()).Disabled(a.busy).Clicked() {
						a.activateConnection()
						toggled = true
						a.treeClosed["table:"+table.ID()] = !a.treeClosed["table:"+table.ID()]
						if !a.treeClosed["table:"+table.ID()] {
							a.loadTableMetadata(table)
						}
					}
					ui.Icon(c, controlIcon(objectIcon(table.Kind))).Size(14, 14).TextColor(k.accent)
					ui.Text(c, table.Name).SingleLine().Grow(1).MinWidth(0).TextColor(k.text)
					menuClicked = a.tableActions(c, k, row, table)
				})
				doubleClicked := row.DoubleClicked()
				if (row.Clicked() || doubleClicked) && !toggled && !menuClicked && !a.busy {
					a.selectedTree = "table:" + table.ID()
					if doubleClicked {
						a.openTableView(table)
					} else {
						a.inspectTable(table)
					}
				}
				if !a.treeClosed["table:"+table.ID()] || a.filter != "" && !scoped {
					a.tableDetailsTree(c, k, table)
				}
			}
			if !flat && counts[group] > visible {
				a.searchMoreRow(c, k, schema, group, tableShift, counts[group])
			}
		}
		if flat && tableCount > explorerObjectLimit {
			a.searchMoreRow(c, k, schema, "objects", shift-1, tableCount)
		}
		if a.explorerEngine() == database.PostgreSQL {
			a.extraGroups(c, k, "schema:"+schema.Name, 2+shift)
		}
		if tableCount == 0 && (scoped || a.explorerEngine() != database.PostgreSQL) {
			message := "No objects"
			if filter != "" || scoped {
				message = "No matching objects"
			}
			ui.Text(c, message).Margin(4, 0, 4, 36).FontSize(11).TextColor(k.muted)
		}
	}
	if shown == 0 && (a.filter == "" || !a.explorerSearchIncomplete(projects, hidden)) {
		message := "No datasets or schemas"
		if a.filter != "" {
			message = "No matching datasets or schemas"
		}
		ui.Text(c, message).Margin(8, 12).FontSize(11).TextColor(k.muted)
	}
}
