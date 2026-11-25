package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) catalogPickerPopover(c *ui.Context, k palette, anchor ui.Element, title, kind string, schemas []database.Schema) {
	p := &a.catalogPicker
	projects := kind == "projects"
	ui.PopoverBase(c.Key("catalog-picker-popover"), anchor, &p.open, func(panel ui.Element) {
		panel.Background(k.panel).Border(1, paneOutline(c)).Radius(8).Clip()
		ui.Column(c).Width(392).Children(func() {
			sectionBar(c, k).Children(func() {
				ui.Text(c, title).SingleLine().MinWidth(0).Grow(1).FontSize(12).FontWeight(600).TextColor(k.text)
				if projects {
					if toolbarAction(c, k, "refresh", "Refresh project list", k.muted).Disabled(p.loading || p.saving).Clicked() {
						a.discoverCatalogProjects()
					}
					if toolbarAction(c, k, "chevron-down", "Expand loaded projects", k.muted).Clicked() {
						for _, project := range a.bigQueryProjects() {
							p.expanded[project] = true
						}
						for project := range p.previewDatasets {
							p.expanded[project] = true
						}
					}
					if toolbarAction(c, k, "collapse", "Collapse projects", k.muted).Clicked() {
						clear(p.expanded)
					}
				}
			})
			filterInput := ui.TextInput(c, &p.filter).Margin(8, 10).Placeholder("Filter " + kind).Label("Filter " + kind)
			filter := strings.ToLower(strings.TrimSpace(p.filter))
			ui.Scroll(c).MaxHeight(420).MinHeight(56).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Padding(0, 10, 8, 10).Gap(2).Children(func() {
					if projects {
						a.catalogProjectChoices(c, k, filter)
					} else {
						a.catalogSchemaChoices(c, k, schemas, kind, filter)
					}
				})
			})
			message := p.err
			if message == "" && projects {
				message = p.discoveryError
			}
			if message != "" {
				ui.Text(c, message).Margin(0, 10, 6, 10).MaxHeight(65).FontSize(11).TextColor(k.danger)
			}
			canApply := !p.saving && !a.busy && !a.connectionStoreBusy
			footer := ui.Row(c).Padding(6, 10).Gap(6).AlignItems(ui.Center).Background(k.header).BorderWidth(1, 0, 0, 0).BorderColor(k.border)
			footer.Children(func() {
				status := "Enter or click outside to apply"
				if p.saving {
					status = "Applying choices…"
				} else if projects && p.loading {
					status = "Finding projects…"
				} else if !canApply {
					status = "Connection is busy"
				}
				ui.Text(c, status).SingleLine().MinWidth(0).Grow(1).FontSize(11).TextColor(k.muted)
				if ui.Button(c, "Cancel").Disabled(p.saving).Clicked() {
					p.open = false
				}
				apply := ui.PrimaryButton(c, "Apply").Disabled(!canApply).Clicked()
				enter := panel.Shortcut(0, ui.KeyEnter) || panel.OverlayShortcut(0, ui.KeyEnter) || filterInput.Submitted()
				if canApply && p.open && (apply || enter || panel.PressedOutside()) {
					a.applyCatalogVisibility()
					if p.err != "" {
						a.status = "Could not update catalog visibility · " + p.err
					}
				}
			})
		})
	})
}

func (a *App) catalogProjectChoices(c *ui.Context, k palette, filter string) {
	p := &a.catalogPicker
	configured := a.bigQueryProjects()
	defaultProject := a.bigQueryProject()
	projects := slices.DeleteFunc(a.catalogPickerProjects(), func(project string) bool {
		return !strings.Contains(strings.ToLower(project), filter)
	})
	choices := make([]*bool, 0, len(projects))
	for _, project := range projects {
		choices = append(choices, catalogChoice(p.projectChoices, project, slices.Contains(configured, project)))
	}
	ui.Row(c).Padding(0, 0, 0, 18).Height(28).AlignItems(ui.Center).Children(func() {
		catalogBulkChoice(c, "projects", filter, choices, p.saving)
	})
	ui.Divider(c)
	for i, project := range projects {
		known := slices.Contains(configured, project)
		ui.Column(c.Key(project)).FillWidth().Children(func() {
			row := ui.Row(c).Height(28).Padding(0, 4).Gap(4).AlignItems(ui.Center).Radius(4)
			if p.expanded[project] {
				row.Background(k.selected)
			}
			row.Children(func() {
				if treeChevron(c, k, p.expanded[project], "Toggle datasets in "+project).Clicked() {
					p.expanded[project] = !p.expanded[project]
					if _, cached := p.previewDatasets[project]; p.expanded[project] && !known && !cached {
						a.previewProjectDatasets(project)
					}
				}
				label := project
				if project == defaultProject {
					label += " (default project)"
				}
				ui.Checkbox(c, choices[i], label).Label("Show project " + project).Disabled(p.saving)
			})
			if p.expanded[project] {
				ui.Column(c).Padding(2, 0, 4, 24).Gap(2).Children(func() {
					a.catalogProjectDatasets(c, k, project, known)
				})
			}
		})
	}
}

func (a *App) catalogProjectDatasets(c *ui.Context, k palette, project string, known bool) {
	p := &a.catalogPicker
	schemas, cached := p.previewDatasets[project]
	if known {
		schemas = nil
		for _, schema := range a.schemas {
			if schema.Project == project {
				schemas = append(schemas, schema)
			}
		}
	}
	if known || cached {
		a.catalogSchemaChoices(c, k, schemas, "datasets", "")
		return
	}
	if p.previewLoading == project {
		ui.Text(c, "Loading datasets…").FontSize(11).TextColor(k.muted)
		return
	}
	if message := p.previewErrors[project]; message != "" {
		ui.Text(c, message).MaxHeight(54).FontSize(11).TextColor(k.danger)
	}
	if ui.Button(c, "Load datasets").Disabled(p.previewLoading != "" || p.saving).Clicked() {
		a.previewProjectDatasets(project)
	}
}

func (a *App) catalogSchemaChoices(c *ui.Context, k palette, schemas []database.Schema, kind, filter string) {
	p := &a.catalogPicker
	var shown []database.Schema
	var choices []*bool
	for _, schema := range schemas {
		if strings.Contains(strings.ToLower(schema.Name), filter) {
			shown = append(shown, schema)
			choices = append(choices, catalogChoice(p.schemaChoices, schema.ID(), true))
		}
	}
	if len(shown) == 0 {
		ui.Text(c, "No "+kind+" found").FontSize(11).TextColor(k.muted)
		return
	}
	ui.Row(c).Height(28).AlignItems(ui.Center).Children(func() {
		catalogBulkChoice(c, kind, filter, choices, p.saving)
	})
	ui.Divider(c)
	for i, schema := range shown {
		label := schema.Name
		if kind == "datasets" && schema.Project == a.bigQueryProject() && schema.Name == a.client.Settings().Dataset {
			label += " (default dataset)"
		}
		ui.Row(c.Key(schema.ID())).Height(26).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, choices[i], label).Label("Show " + strings.TrimSuffix(kind, "s") + " " + schema.Name).Disabled(p.saving)
		})
	}
}

func catalogBulkChoice(c *ui.Context, kind, filter string, choices []*bool, disabled bool) {
	label := "All " + kind
	if filter != "" {
		label = "Matching " + kind
	}
	var selected int
	for _, choice := range choices {
		if *choice {
			selected++
		}
	}
	state := ui.CheckOff
	if selected != 0 {
		state = ui.CheckMixed
		if selected == len(choices) {
			state = ui.CheckOn
		}
	}
	if ui.CheckboxState(c, &state, label).Disabled(disabled || len(choices) == 0).Changed() {
		for _, choice := range choices {
			*choice = state == ui.CheckOn
		}
	}
}

func (a *App) previewProjectDatasets(project string) {
	p := &a.catalogPicker
	if a.win == nil || p.previewLoading != "" || p.saving {
		return
	}
	if p.previewDatasets == nil {
		p.previewDatasets = make(map[string][]database.Schema)
		p.previewErrors = make(map[string]string)
	}
	p.previewLoading = project
	delete(p.previewErrors, project)
	client, win := a.client, a.win
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	go func() {
		defer cancel()
		schemas, err := client.BigQueryProjectDatasets(ctx, project)
		win.Update(func() {
			if a.client != client || a.closed {
				return
			}
			p.previewLoading = ""
			if err != nil {
				p.previewErrors[project] = err.Error()
				return
			}
			total := len(schemas)
			for _, cached := range p.previewDatasets {
				total += len(cached)
			}
			if total > database.CatalogLimit {
				clear(p.previewDatasets)
			}
			p.previewDatasets[project] = schemas
		})
	}()
}
