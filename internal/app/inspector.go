package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) inspectTable(t database.Table, force ...bool) {
	a.openInspector()
	a.inspectorObject = nil
	a.inspectorColumn = nil
	a.inspectorDefinition = nil
	a.setObjectPath(a.tableObjectPath(t)...)
	a.inspectorTable, a.activeTable = t, t.ID()
	a.loadTableMetadata(t, force...)
}

func (a *App) loadTableMetadata(t database.Table, force ...bool) {
	if a.metadata == nil {
		a.metadata = map[string]database.TableMetadata{}
	}
	if a.metadataErrors == nil {
		a.metadataErrors = map[string]string{}
	}
	if a.metadataLoading == nil {
		a.metadataLoading = map[string]bool{}
	}
	refresh := len(force) > 0 && force[0]
	if m, ok := a.metadata[t.ID()]; ok && !refresh && m.Fresh() {
		return
	}
	if a.client == nil || a.win == nil {
		return
	}
	if a.metadataLoading[t.ID()] {
		return
	}
	a.metadataLoading[t.ID()] = true
	delete(a.metadataErrors, t.ID())
	client, win := a.client, a.win
	runner, pipeline := a.catalogRunner()
	version := a.metadataVersion
	parent := a.connectionContext()
	var refreshSections []string
	if refresh && client != nil && client.Engine() == database.BigQuery {
		for _, section := range []string{"indexes", "partitions", "policies", "privileges"} {
			if a.tableSectionsOpen[sectionKey(t, section)] {
				refreshSections = append(refreshSections, section)
			}
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		var m database.TableMetadata
		var err error
		if pipeline != nil {
			m, err = pipeline.Table(ctx, t, len(force) > 0 && force[0])
		} else {
			m, err = runner.TableMetadata(ctx, t)
		}
		sectionErrors := map[string]string{}
		if err == nil && pipeline != nil {
			for _, section := range refreshSections {
				updated, e := pipeline.Section(ctx, t, section, true)
				if e != nil {
					sectionErrors[sectionKey(t, section)] = e.Error()
				} else {
					m = updated
				}
			}
		}
		warning := ""
		if pipeline != nil {
			warning = pipeline.Warning()
		}
		win.Update(func() {
			if a.closed || a.client != client || version != a.metadataVersion {
				return
			}
			delete(a.metadataLoading, t.ID())
			a.cacheWarning = warning
			if a.tableSectionErrors == nil {
				a.tableSectionErrors = map[string]string{}
			}
			for key, message := range sectionErrors {
				a.tableSectionErrors[key] = message
			}
			if err != nil {
				a.metadataErrors[t.ID()] = err.Error()
			} else {
				if a.metadata == nil {
					a.metadata = map[string]database.TableMetadata{}
				}
				a.metadata[t.ID()] = m
				a.refreshColumnInspection(t, m)
				a.refreshTableObjectInspection(t, m)
				for i := range a.schemas {
					for j := range a.schemas[i].Tables {
						table := &a.schemas[i].Tables[j]
						if table.ID() == t.ID() {
							table.Kind = m.Kind
							table.Partition = m.Partition
							table.Clustering = m.Clustering
						}
					}
				}
			}
		})
	}()
}

func columnLabel(col database.Column) string {
	key := ""
	if col.PrimaryKey {
		key = "PK · "
	}
	if col.ForeignKey {
		key += "FK · "
	}
	return key + col.Name + "  " + col.Type
}

func columnRow(c *ui.Context, k palette, col database.Column) {
	ui.Row(c).Gap(5).AlignItems(ui.Center).Tooltip(columnTooltip(col)).Children(func() {
		columnMarkers(c, k, col)
		dataTypeIcon(c, k, col)
		ui.Text(c, col.Name).SingleLine().MinWidth(0).FontSize(12).TextColor(k.text)
	})
}

func (a *App) inspector(c *ui.Context, k palette) {
	_, height := c.Size()
	height -= 72
	ui.Column(c).FillWidth().Height(height).Shrink(0).MinWidth(0).Radius(10).Border(1, paneOutline(c)).Clip().Background(k.panel).Label("Right sidebar content").Children(func() {
		a.rightSidebarHeader(c, k, true)
		if a.inspectorDefinition != nil {
			a.tableObjectInspector(c, k)
			return
		}
		if a.inspectorColumn != nil {
			a.columnInspector(c, k)
			return
		}
		if a.inspectorObject != nil {
			a.catalogInspector(c, k)
			return
		}
		if a.activeTable == "" {
			ui.Column(c).Grow(1).FillWidth().Padding(24).Gap(12).Center().Children(func() {
				ui.Icon(c, controlIcon("inspector")).Size(28, 28).TextColor(k.muted)
				ui.Text(c, "Select an object").FontSize(15).FontWeight(600).TextColor(k.text)
				ui.Text(c, "Choose a table, column, or database object to view its details.").FontSize(12).TextColor(k.muted)
			})
			return
		}
		inspectorHeading(c, k, a.inspectorTable.Name, strings.TrimSuffix(tableGroup(a.inspectorTable), "s"), a.inspectorTable.Schema, objectIcon(a.inspectorTable.Kind), "Inspected table "+a.inspectorTable.Label(), func() {
			if editablePostgreSQLTable(a.client.Engine(), a.inspectorTable) {
				if toolbarAction(c, k, "settings", "Edit table", k.muted).Disabled(a.client == nil).Clicked() {
					a.openTableAction(a.inspectorTable, "Properties")
				}
			}
		})
		if ui.Button(c, "Query table").Margin(0, 12).Disabled(a.busy || a.client == nil).Clicked() {
			if a.currentEditor() == nil {
				a.addQuery("")
			}
			a.setQuery(a.inspectorTable.Preview(a.client.Engine()))
			a.runSQL(a.query, false)
		}
		ui.Row(c).Children(func() {
			if tabLabel(c, k, "Columns", !a.inspectorDDL && !a.inspectorProperties).Clicked() {
				a.inspectorDDL = false
				a.inspectorProperties = false
			}
			if tabLabel(c, k, "DDL", a.inspectorDDL).Clicked() {
				a.inspectorDDL = true
				a.inspectorProperties = false
			}
			if tabLabel(c, k, "Properties", a.inspectorProperties).Clicked() {
				a.inspectorProperties = true
				a.inspectorDDL = false
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(12).Gap(10).Children(func() {
				id := a.inspectorTable.ID()
				if a.metadataLoading[id] {
					ui.Text(c, "Loading metadata…").TextColor(k.muted)
					return
				}
				if message := a.metadataErrors[id]; message != "" {
					ui.Text(c, message).TextColor(k.danger)
					if ui.Button(c, "Retry metadata").Clicked() {
						a.inspectTable(a.inspectorTable, true)
					}
					return
				}
				m, ok := a.metadata[id]
				if !ok {
					if ui.Button(c, "Load metadata").Clicked() {
						a.inspectTable(a.inspectorTable)
					}
					return
				}
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					for _, metric := range []struct{ label, value string }{{"Columns", fmt.Sprint(len(m.Columns))}, {"Estimated rows", m.RowEstimate}} {
						value := metric.value
						if value == "" {
							value = "—"
						}
						ui.Column(c).Grow(1).MinWidth(0).Padding(10).Gap(5).Radius(6).Background(k.header).Children(func() {
							ui.Text(c, metric.label).FontSize(10).TextColor(k.muted)
							ui.Text(c, value).SingleLine().MinWidth(0).FontSize(15).FontWeight(600).TextColor(k.text).Tooltip(value)
						})
					}
				})
				if !m.FetchedAt.IsZero() {
					label := "Live · "
					if m.FromCache {
						label = "Cached · "
					}
					ui.Text(c, label+m.FetchedAt.Local().Format("02 Jan 15:04")).FontSize(10).TextColor(k.muted)
				}
				if m.Partition.Strategy != "" || m.Partition.IsPartition {
					inspectorField(c, k, "Partitioning", m.Partition.Summary())
				}
				if m.Partition.RequireFilter {
					ui.Text(c, "Partition filter required").FontSize(10).TextColor(k.accent)
				}
				if m.Partition.ExpirationMillis > 0 {
					ui.Text(c, fmt.Sprintf("Partition expiry: %s", time.Duration(m.Partition.ExpirationMillis)*time.Millisecond)).FontSize(10).TextColor(k.muted)
				}
				if len(m.Clustering) > 0 {
					ui.Text(c, "Clustering: "+strings.Join(m.Clustering, " → ")).FontSize(11).TextColor(k.muted)
				}
				if a.inspectorProperties {
					a.propertyTree(c, k, m.Properties, "inspector:"+id, 0, 0)
				} else if a.inspectorDDL {
					inspectorCode(c, k, "DDL", m.DDL)
				} else {
					a.columnTree(c, k, m.Columns, "inspector:"+id, "", 0, 0, a.inspectorTable)
					for _, group := range []struct {
						name        string
						definitions []database.Definition
					}{{"Constraints", m.Constraints}, {"Indexes", m.Indexes}, {"Triggers", m.Triggers}} {
						if len(group.definitions) == 0 {
							continue
						}
						inspectorSection(c, k, group.name)
						for _, d := range group.definitions {
							inspectorField(c, k, d.Name, d.SQL)
						}
					}
				}
				if m.Note != "" {
					ui.Text(c, m.Note).FontSize(11).TextColor(k.muted)
				}
			})
		})
	})
}
