package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) connectionSettings() database.Config {
	// Explorer projects can change while the query client's settings stay fixed.
	if saved, ok := a.matchingConnection(); ok {
		return saved.Config
	}
	return a.client.Settings()
}

func mergeBigQueryCatalog(current, refreshed []database.Schema, projects []string) []database.Schema {
	all := slices.Clone(refreshed)
	for _, schema := range current {
		if !slices.Contains(projects, schema.Project) {
			all = append(all, schema)
		}
	}
	return all
}

func (a *App) bigQueryProjects() []string {
	if a.client != nil {
		return a.connectionSettings().BigQueryProjects()
	}
	projects := []string{}
	for _, schema := range a.schemas {
		if !slices.Contains(projects, schema.Project) {
			projects = append(projects, schema.Project)
		}
	}
	return projects
}

func (a *App) openBigQueryProject() {
	if a.client == nil || a.client.Engine() != database.BigQuery || a.busy || a.connectionStoreBusy {
		return
	}
	a.activateConnection()
	a.bigQueryProjectID, a.bigQueryProjectError = "", ""
	a.showBigQueryProject = true
}

func (a *App) bigQueryProjectDialog(c *ui.Context, k palette) {
	ui.Modal(c.Key("bigquery-project-dialog"), &a.showBigQueryProject, func() {
		ui.Column(c).Width(400).Gap(12).Children(func() {
			ui.Text(c, "Add BigQuery project").FontSize(18).FontWeight(600).TextColor(k.text)
			connectionField(c, k, "Project ID", &a.bigQueryProjectID, false)
			ui.Text(c, "Uses this connection's Google credentials. Queries continue to use its billing project and billing cap.").FontSize(11).TextColor(k.muted)
			if a.bigQueryProjectError != "" {
				ui.Text(c, a.bigQueryProjectError).MaxHeight(90).FontSize(11).TextColor(k.danger)
			}
			if a.busy {
				ui.Text(c, "Checking project access and saving…").FontSize(11).TextColor(k.muted)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Disabled(a.busy).Clicked() {
					a.showBigQueryProject = false
				}
				if ui.PrimaryButton(c, "Add project").Disabled(a.busy || a.connectionStoreBusy).Clicked() {
					a.addBigQueryProject()
				}
			})
		})
	})
}

func addBigQueryProjectConfig(cfg database.Config, project string) (database.Config, error) {
	project = strings.TrimSpace(project)
	if err := database.ValidateBigQueryProject(project); err != nil {
		return cfg, err
	}
	projects := cfg.BigQueryProjects()
	if slices.Contains(projects, project) {
		return cfg, fmt.Errorf("project %s is already in this connection", project)
	}
	if len(projects) >= database.MaxBigQueryProjects {
		return cfg, fmt.Errorf("a connection supports up to %d projects", database.MaxBigQueryProjects)
	}
	cfg.AdditionalProjects = append(slices.Clone(cfg.AdditionalProjects), project)
	if slices.Contains(cfg.HiddenProjects, project) {
		cfg.HiddenProjects = slices.DeleteFunc(slices.Clone(cfg.HiddenProjects), func(id string) bool { return id == project })
		// The original data project is already supplied by the connection settings.
		if project == cfg.DatasetProject || cfg.DatasetProject == "" && project == cfg.Project {
			cfg.AdditionalProjects = slices.DeleteFunc(cfg.AdditionalProjects, func(id string) bool { return id == project })
		}
	}
	return cfg, nil
}

func removeBigQueryProjectConfig(cfg database.Config, project string) (database.Config, error) {
	if !slices.Contains(cfg.BigQueryProjects(), project) {
		return cfg, fmt.Errorf("project %s is not in this connection", project)
	}
	cfg.AdditionalProjects = slices.DeleteFunc(slices.Clone(cfg.AdditionalProjects), func(id string) bool { return id == project })
	if project == cfg.DatasetProject || cfg.DatasetProject == "" && project == cfg.Project {
		cfg.HiddenProjects = append(slices.Clone(cfg.HiddenProjects), project)
	}
	return cfg, nil
}

func visibleBigQueryCatalog(schemas []database.Schema, projects []string) []database.Schema {
	return slices.DeleteFunc(slices.Clone(schemas), func(s database.Schema) bool { return !slices.Contains(projects, s.Project) })
}

func (a *App) removeBigQueryProject(project string) {
	if a.busy || a.connectionStoreBusy || a.closed || a.client == nil || a.client.Engine() != database.BigQuery || a.win == nil {
		return
	}
	cfg, err := removeBigQueryProjectConfig(a.connectionSettings(), project)
	if err != nil {
		a.status = err.Error()
		return
	}
	name := a.connectionLabel()
	connections, err := upsertConnection(a.connections, name, savedConnection{Name: name, Config: cfg})
	if err != nil {
		a.status = err.Error()
		return
	}
	client, pipeline, win := a.client, a.introspector, a.win
	a.busy = true
	a.started = time.Time{}
	a.setConnectionStoreBusy(true)
	go func() {
		path, err := connectionPath()
		if err == nil {
			err = writeConnections(path, connections)
		}
		if err == nil && pipeline != nil {
			pipeline.ForgetBigQueryProject(project)
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
			a.busy = false
			if err != nil {
				a.status = "Could not remove project: " + err.Error()
				a.pendingToasts = append(a.pendingToasts, ui.Toast{Title: "Could not remove project", Description: err.Error(), Type: "error"})
				return
			}
			a.pruneBigQueryProject(project)
			a.status = "Project removed from tree · " + project
		})
	}()
}

func removeBigQueryProjectKeys[V any](values map[string]V, project string) {
	for key := range values {
		owner, _, _ := strings.Cut(key, "\x00")
		for owner != project {
			previous := owner
			for _, prefix := range []string{"schema:", "table:", "group:", "explorer:", "inspector:", "column:", "table-object:", "definition:"} {
				owner = strings.TrimPrefix(owner, prefix)
			}
			if owner == previous {
				break
			}
		}
		if owner == project || key == "project:"+project {
			delete(values, key)
		}
	}
}

func (a *App) pruneBigQueryProject(project string) {
	a.schemas = slices.DeleteFunc(slices.Clone(a.schemas), func(s database.Schema) bool { return s.Project == project })
	for _, tab := range a.tabs {
		tab.querySchemas = slices.DeleteFunc(slices.Clone(tab.querySchemas), func(s database.Schema) bool { return s.Project == project })
	}
	removeBigQueryProjectKeys(a.treeClosed, project)
	removeBigQueryProjectKeys(a.schemaErrors, project)
	removeBigQueryProjectKeys(a.schemaLoading, project)
	removeBigQueryProjectKeys(a.metadata, project)
	removeBigQueryProjectKeys(a.metadataErrors, project)
	removeBigQueryProjectKeys(a.metadataLoading, project)
	removeBigQueryProjectKeys(a.tableSectionsOpen, project)
	removeBigQueryProjectKeys(a.tableSectionLoading, project)
	removeBigQueryProjectKeys(a.tableSectionErrors, project)
	removeBigQueryProjectKeys(a.propertyOpen, project)
	removeBigQueryProjectKeys(a.fieldOpen, project)
	if a.inspectorTable.Project == project {
		a.inspectorTable = database.Table{}
		a.inspectorColumn, a.inspectorDefinition = nil, nil
	}
	if strings.HasPrefix(a.activeTable, project+"\x00") {
		a.activeTable = ""
	}
	if len(a.objectPath) > 2 && a.objectPath[2] == project {
		a.selectedTree = "connection"
		a.setObjectPath(a.connectionObjectPath()...)
	}
}

func (a *App) addBigQueryProject() {
	if a.busy || a.connectionStoreBusy || a.closed || a.client == nil || a.client.Engine() != database.BigQuery || a.win == nil {
		return
	}
	project := strings.TrimSpace(a.bigQueryProjectID)
	cfg, err := addBigQueryProjectConfig(a.connectionSettings(), project)
	if err != nil {
		a.bigQueryProjectError = err.Error()
		return
	}
	name := a.connectionLabel()
	connections, err := upsertConnection(a.connections, name, savedConnection{Name: name, Config: cfg})
	if err != nil {
		a.bigQueryProjectError = err.Error()
		return
	}
	client, pipeline, win := a.client, a.introspector, a.win
	datasetCount := len(a.schemas)
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.busy, a.cancel = true, cancel
	a.started = time.Time{}
	a.bigQueryProjectError = ""
	a.setConnectionStoreBusy(true)
	go func() {
		defer cancel()
		var schemas []database.Schema
		var err error
		if pipeline != nil {
			schemas, err = pipeline.BigQueryProject(ctx, project, true)
		} else {
			schemas, err = client.BigQueryProjectDatasets(ctx, project)
		}
		if err == nil && datasetCount+len(schemas) > database.CatalogLimit {
			err = fmt.Errorf("connection exceeds the 10,000 dataset display limit")
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
			a.busy, a.cancel = false, nil
			if err != nil {
				a.bigQueryProjectError = err.Error()
				return
			}
			a.applyCatalog(append(slices.Clone(a.schemas), schemas...), false)
			a.showBigQueryProject = false
			a.status = "Project added · " + project
		})
	}()
}

func (a *App) refreshBigQueryProject(project string) {
	if a.busy || a.closed || a.client == nil || a.win == nil {
		return
	}
	client, pipeline, win := a.client, a.introspector, a.win
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.busy, a.cancel, a.status = true, cancel, "Refreshing "+project+"…"
	a.started = time.Time{}
	go func() {
		defer cancel()
		var schemas []database.Schema
		var err error
		if pipeline != nil {
			schemas, err = pipeline.BigQueryProject(ctx, project, true)
		} else {
			schemas, err = client.BigQueryProjectDatasets(ctx, project)
		}
		win.Update(func() {
			if a.closed || a.client != client {
				return
			}
			a.busy, a.cancel = false, nil
			if err != nil {
				a.status = "Refresh failed: " + err.Error()
				return
			}
			all := []database.Schema{}
			for _, schema := range a.schemas {
				if schema.Project != project {
					all = append(all, schema)
				} else {
					for _, table := range schema.Tables {
						delete(a.metadata, table.ID())
					}
				}
			}
			a.applyCatalog(append(all, schemas...), false)
			a.status = "Project refreshed · " + project
		})
	}()
}
