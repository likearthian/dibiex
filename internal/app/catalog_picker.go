package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type catalogPicker struct {
	open, loading, discovered, saving bool
	err, discoveryError               string
	projects                          []string
	scope, filter                     string
	expanded                          map[string]bool
	previewDatasets                   map[string][]database.Schema
	previewLoading                    string
	previewErrors                     map[string]string
	projectChoices, schemaChoices     map[string]*bool
	local                             database.Config
}

func (a *App) explorerVisibility() database.Config {
	if a.client == nil {
		return database.Config{}
	}
	if a.client.Engine() != database.SQLite {
		if saved, ok := a.matchingConnection(); ok {
			return saved.Config
		}
	}
	return a.catalogPicker.local
}

func (a *App) catalogPickerScope(key string) (kind, project string) {
	if a.client == nil {
		return "", ""
	}
	switch {
	case key == "connection" && a.explorerEngine() == database.BigQuery:
		return "projects", ""
	case strings.HasPrefix(key, "project:"):
		return "datasets", strings.TrimPrefix(key, "project:")
	case strings.HasPrefix(key, "database:"):
		return "schemas", ""
	case key == "catalog-databases":
		return "databases", ""
	}
	return "", ""
}

func (a *App) catalogPickerProjects() []string {
	projects := a.bigQueryProjects()
	configuredCount := len(projects)
	for _, project := range a.catalogPicker.projects {
		if !slices.Contains(projects[:configuredCount], project) {
			projects = append(projects, project)
		}
	}
	return projects
}

func (a *App) catalogPickerButton(c *ui.Context, k palette, key, name, kind, project string) {
	p := &a.catalogPicker
	cfg := a.explorerVisibility()
	projects := kind == "projects"
	enabled, total := 0, 0
	var schemas []database.Schema
	if projects {
		configured := a.bigQueryProjects()
		total = len(a.catalogPickerProjects())
		for _, id := range configured {
			if !cfg.ExplorerHiddenProjects[id] {
				enabled++
			}
		}
	} else {
		for _, schema := range a.schemas {
			if project == "" || schema.Project == project {
				schemas = append(schemas, schema)
				if !cfg.ExplorerHiddenSchemas[schema.ID()] {
					enabled++
				}
			}
		}
		total = len(schemas)
	}
	count := fmt.Sprintf("%d of %d", enabled, total)
	label := "Choose " + kind + " for " + name
	button := ui.ButtonBase(c.Key("catalog-picker")).Height(22).Padding(0, 5).Shrink(0).Radius(4).Border(1, k.border).Center().Label(label + " · " + count).Tooltip(label)
	if button.Hovered() || p.open && p.scope == key {
		button.Background(k.selected)
	}
	button.Children(func() { ui.Text(c, count).SingleLine().FontSize(11).TextColor(k.muted) })
	if button.Clicked() && !p.saving {
		p.open = !p.open || p.scope != key
		p.scope = key
		if p.open {
			a.activateConnection()
			p.projectChoices = catalogChoices(cfg.ExplorerHiddenProjects)
			p.schemaChoices = catalogChoices(cfg.ExplorerHiddenSchemas)
			p.expanded = make(map[string]bool)
			p.filter = ""
			p.err = ""
			if projects && !p.discovered {
				a.discoverCatalogProjects()
			}
		}
	}
	if p.scope != key {
		return
	}
	a.catalogPickerPopover(c, k, button, label, kind, schemas)
}

func catalogChoices(hidden map[string]bool) map[string]*bool {
	choices := make(map[string]*bool, len(hidden))
	for id := range hidden {
		choices[id] = new(false)
	}
	return choices
}

func catalogChoice(choices map[string]*bool, id string, initial bool) *bool {
	if choices[id] == nil {
		choices[id] = new(initial)
	}
	return choices[id]
}

func hiddenCatalogChoices(choices map[string]*bool) map[string]bool {
	hidden := make(map[string]bool)
	for id, shown := range choices {
		if !*shown {
			hidden[id] = true
		}
	}
	return hidden
}

func (a *App) discoverCatalogProjects() {
	p := &a.catalogPicker
	if a.win == nil || p.loading {
		return
	}
	p.loading, p.discoveryError = true, ""
	client, win := a.client, a.win
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	go func() {
		defer cancel()
		projects, err := client.BigQueryAccessibleProjects(ctx)
		win.Update(func() {
			if a.client != client || a.closed {
				return
			}
			p.loading = false
			if err != nil {
				p.discoveryError = err.Error()
				return
			}
			p.projects, p.discovered = projects, true
		})
	}()
}

func (a *App) applyCatalogVisibility() {
	p := &a.catalogPicker
	cfg := a.connectionSettings()
	cfg.ExplorerHiddenProjects = make(map[string]bool)
	cfg.ExplorerHiddenSchemas = hiddenCatalogChoices(p.schemaChoices)
	configured := cfg.BigQueryProjects()
	var add []string
	for _, id := range slices.Sorted(maps.Keys(p.projectChoices)) {
		shown := *p.projectChoices[id]
		if slices.Contains(configured, id) {
			if !shown {
				cfg.ExplorerHiddenProjects[id] = true
			}
		} else if shown {
			add = append(add, id)
		}
	}
	current := a.explorerVisibility()
	if len(add) == 0 && maps.Equal(cfg.ExplorerHiddenProjects, current.ExplorerHiddenProjects) &&
		maps.Equal(cfg.ExplorerHiddenSchemas, current.ExplorerHiddenSchemas) {
		p.open = false
		return
	}
	for _, project := range add {
		var err error
		cfg, err = addBigQueryProjectConfig(cfg, project)
		if err != nil {
			p.err = err.Error()
			return
		}
	}
	saved, persist := a.matchingConnection()
	persist = persist && cfg.Engine != database.SQLite
	if len(add) == 0 && !persist {
		p.local, p.open = cfg, false
		return
	}
	if a.win == nil {
		return
	}
	name := a.connectionLabel()
	if persist {
		name = saved.Name
	}
	originalName := ""
	if persist {
		originalName = name
	}
	connections, err := upsertConnection(a.connections, originalName, savedConnection{Name: name, Config: cfg})
	if err != nil {
		p.err = err.Error()
		return
	}
	client, win := a.client, a.win
	datasetCount := len(a.schemas)
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	p.saving, p.err = true, ""
	a.setConnectionStoreBusy(true)
	a.busy, a.cancel = true, cancel
	go func() {
		defer cancel()
		var added []database.Schema
		var err error
		for _, project := range add {
			var datasets []database.Schema
			datasets, err = client.BigQueryProjectDatasets(ctx, project)
			if err != nil {
				break
			}
			if datasetCount+len(added)+len(datasets) > database.CatalogLimit {
				err = fmt.Errorf("connection exceeds the 10,000 dataset display limit")
				break
			}
			added = append(added, datasets...)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			var path string
			path, err = connectionPath()
			if err == nil {
				err = writeConnections(path, connections)
			}
		}
		win.Update(func() {
			a.setConnectionStoreBusy(false)
			if a.closed {
				return
			}
			if err == nil {
				a.setSavedConnections(connections)
			}
			if a.client != client {
				return
			}
			a.busy, a.cancel, p.saving = false, nil, false
			if err != nil {
				p.err = err.Error()
				a.status = "Could not update catalog visibility · " + err.Error()
				return
			}
			if len(add) > 0 {
				a.applyCatalog(append(slices.Clone(a.schemas), added...), false)
			}
			p.open = false
		})
	}()
}
