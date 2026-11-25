package app

import (
	"fmt"
	"strings"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

const explorerObjectLimit = 100

type explorerSchemaSearch struct {
	schemaID string
	query    string
	focus    bool
}

func (a *App) discoverExplorerSearch(c *ui.Context, k palette) {
	if a.win == nil || strings.TrimSpace(a.filter) == "" || a.client == nil {
		return
	}
	hidden := a.explorerVisibility().ExplorerHiddenSchemas
	next, pending, failed := -1, false, false
	for i, schema := range a.schemas {
		id := schema.ID()
		if schema.Loaded || hidden[id] {
			continue
		}
		switch {
		case a.schemaIsLoading(id):
			pending = true
		case a.schemaErrors[id] != "":
			failed = true
		case next == -1:
			next = i
		}
	}
	if next >= 0 && !pending {
		a.loadSchema(next)
	}
	if pending || next >= 0 {
		ui.Text(c, "Searching unopened schemas and datasets…").Margin(0, 8, 4, 8).FontSize(11).TextColor(k.muted)
	} else if failed {
		ui.Text(c, "Some schemas or datasets could not be searched.").Margin(0, 8, 4, 8).FontSize(11).TextColor(k.danger)
		if ui.Button(c.Key("retry-explorer-search"), "Retry search").Margin(0, 8, 4, 8).Clicked() {
			for _, schema := range a.schemas {
				if !schema.Loaded && !hidden[schema.ID()] {
					delete(a.schemaErrors, schema.ID())
				}
			}
		}
	}
}

func (a *App) explorerSearchIncomplete(projects []string, hidden map[string]bool) bool {
	if a.win == nil {
		return false
	}
	for _, schema := range a.schemas {
		if !schema.Loaded && !hidden[schema.ID()] && (len(projects) == 0 || schema.Project == projects[0]) {
			return true
		}
	}
	return false
}

func schemaSearchLabel(schema database.Schema) string {
	if schema.Project != "" {
		return schema.Project + "." + schema.Name
	}
	return schema.Name
}

func (a *App) scopedSearchOpens(key string) bool {
	id := a.explorerSearch.schemaID
	if id == "" {
		return false
	}
	project, _, _ := strings.Cut(id, "\x00")
	return key == "connection" || strings.HasPrefix(key, "database:") || key == "project:"+project || key == "schema:"+id || strings.HasPrefix(key, "group:"+id+":")
}

func (a *App) searchSchema(schema database.Schema) {
	a.activateConnection()
	a.filter = ""
	if a.explorerSearch.schemaID != schema.ID() {
		a.explorerSearch = explorerSchemaSearch{schemaID: schema.ID()}
	}
	a.explorerSearch.focus = true
}

func (a *App) schemaSearchInput(c *ui.Context, k palette, schema database.Schema, shift int) {
	ui.Column(c.Key("schema-search:"+schema.ID())).FillWidth().MinWidth(0).Padding(4, 8, 6, min(float32(26+shift*18), max(float32(8), a.explorerWidth-160))).Gap(4).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Search in "+schema.Name).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted).Tooltip(schemaSearchLabel(schema))
			if toolButton(c, k, "x", "Close schema search", false).Clicked() {
				a.explorerSearch = explorerSchemaSearch{}
			}
		})
		input := ui.TextInput(c.Key("schema-object-filter"), &a.explorerSearch.query).FillWidth().MinWidth(0).Placeholder("Search table or view names…").Label("Search within schema or dataset").Tooltip("Search all loaded table and view names in " + schema.Name)
		if a.explorerSearch.focus {
			input.Focus().ScrollIntoView()
			a.explorerSearch.focus = false
		}
	})
}

func (a *App) searchMoreRow(c *ui.Context, k palette, schema database.Schema, group string, shift int, total int) {
	indent := min(float32(68+shift*18), max(float32(8), a.explorerWidth-100))
	ui.Column(c.Key("search-more:"+schema.ID()+":"+group)).FillWidth().MinWidth(0).Padding(2, 8, 6, indent).Gap(2).Children(func() {
		ui.Text(c, fmt.Sprintf("Showing %d of %d", explorerObjectLimit, total)).SingleLine().FontSize(10).TextColor(k.muted)
		if a.explorerSearch.schemaID == schema.ID() && a.explorerSearch.query != "" {
			ui.Text(c, "Refine search to see other matches").SingleLine().FontSize(10).TextColor(k.muted)
		}
		link := ui.ButtonBase(c).Height(24).FillWidth().MinWidth(0).Label("Search for more in " + schemaSearchLabel(schema) + " " + group).Tooltip("Search within " + schema.Name)
		link.Children(func() { ui.Text(c, "Search for more…").SingleLine().FontSize(12).TextColor(k.accent) })
		if link.Clicked() {
			a.searchSchema(schema)
		}
	})
}
