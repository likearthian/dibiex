package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func sectionKey(t database.Table, section string) string { return t.ID() + "\x00" + section }

func (a *App) tableBranch(c *ui.Context, k palette, t database.Table, section, title, icon string, count int) bool {
	if a.tableSectionsOpen == nil {
		a.tableSectionsOpen = map[string]bool{}
	}
	key := sectionKey(t, section)
	open := a.tableSectionsOpen[key]
	if a.filter != "" && section == "columns" {
		open = true
	}
	row := ui.Row(c.Key("table-branch:"+key)).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, min(float32(86)+a.explorerOffset(), max(float32(8), a.explorerWidth-100))).Gap(5).AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Select " + title + " for " + t.Label())
	if a.selectedTree == "table-branch:"+key {
		row.Background(k.border).Radius(4)
	} else if row.Hovered() {
		row.Background(k.border)
	}
	toggled := false
	row.Children(func() {
		if treeChevron(c, k, open, "Toggle "+title+" for "+t.Label()).Clicked() {
			a.activateConnection()
			a.tableSectionsOpen[key] = !open
			toggled = true
			if !open && a.metadata[t.ID()].Sections[section].State == "unloaded" {
				a.loadTableSection(t, section, false)
			}
		}
		ui.Icon(c, controlIcon("tree-folder")).Size(15, 15).Shrink(0).TextColor(k.accent)
		ui.Row(c).MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Text(c, strings.ToLower(title)).Label(title).SingleLine().MinWidth(0).TextColor(k.text)
			if count >= 0 {
				ui.Text(c, fmt.Sprint(count)).Shrink(0).FontSize(10).TextColor(k.muted)
			}
		})
		ui.Spacer(c).MinWidth(0)
		if section != "columns" && section != "clustering" {
			objectActions(c, k, row, title+" for "+t.Label(), func(m *ui.Menu) {
				stateKey := section
				if section == "keys" {
					stateKey = "constraints"
				}
				state := a.metadata[t.ID()].Sections[stateKey]
				if m.Item("Refresh").Disabled(a.busy || a.client == nil || a.metadataLoading[t.ID()] || state.State == "unsupported").Chosen() {
					a.refreshTableBranch(t, section)
				}
			})
		}
	})
	if (row.Clicked() || row.Shortcut(0, ui.KeyEnter)) && !toggled {
		a.activateConnection()
		a.selectedTree = "table-branch:" + key
		a.setObjectPath(append(a.tableObjectPath(t), strings.ToLower(title))...)
	}
	return open
}

func (a *App) tableDetailsTree(c *ui.Context, k palette, t database.Table) {
	indent := float32(104) + a.explorerOffset()
	m, ok := a.metadata[t.ID()]
	if !ok {
		message := "Expand the table to load metadata"
		if a.metadataLoading[t.ID()] {
			message = "Loading table metadata…"
		}
		if err := a.metadataErrors[t.ID()]; err != "" {
			message = "Metadata unavailable: " + err
		}
		ui.Text(c, message).Margin(4, 8, 4, 86+a.explorerOffset()).FontSize(10).TextColor(k.muted)
		return
	}
	if a.tableBranch(c, k, t, "columns", "Columns", "table", len(m.Columns)) {
		a.columnTree(c, k, m.Columns, "explorer:"+t.ID(), "", 0, indent, t)
	}
	for _, group := range []struct{ key, title, icon string }{{"keys", "Keys", "key"}, {"indexes", "Indexes", "index"}, {"triggers", "Triggers", "trigger"}, {"partitions", "Partitions", "partitioned-table"}, {"constraints", "Constraints", "constraint"}, {"policies", "Policies", "shield"}, {"privileges", "Privileges", "key"}, {"rules", "Rules", "table"}} {
		stateKey := group.key
		if group.key == "keys" {
			stateKey = "constraints"
		}
		state := m.Sections[stateKey]
		items := tableDefinitionItems(m, group.key)
		if state.State == "unsupported" || state.State == "loaded" && len(items) == 0 && (group.key != "partitions" || m.Partition.Strategy == "") {
			continue
		}
		count := len(items)
		if state.State == "unloaded" || state.State == "unavailable" || state.State == "unsupported" {
			count = -1
		}
		if !a.tableBranch(c, k, t, group.key, group.title, group.icon, count) {
			continue
		}
		key := sectionKey(t, group.key)
		if a.tableSectionLoading[key] {
			ui.Text(c, "Loading…").Margin(2, 8, 2, indent).FontSize(10).TextColor(k.muted)
		}
		if state.State == "unsupported" {
			ui.Text(c, state.Message).Margin(2, 8, 2, indent).FontSize(10).TextColor(k.muted)
			continue
		}
		err := a.tableSectionErrors[key]
		if err == "" && (state.State == "unavailable" || state.State == "partial") {
			err = state.Message
		}
		if err != "" {
			ui.Text(c, "Unavailable: "+err).Margin(2, 8, 2, indent).MaxHeight(70).FontSize(10).TextColor(k.danger)
		}
		if state.State == "unloaded" || err != "" {
			if state.Message != "" && err == "" {
				ui.Text(c, state.Message).Margin(2, 8, 2, indent).FontSize(10).TextColor(k.muted)
			}
			if ui.Button(c.Key("load:"+key), "Load "+strings.ToLower(group.title)).Margin(2, 8, 2, indent).Disabled(a.busy || a.client == nil || a.tableSectionLoading[key]).Clicked() {
				a.refreshTableBranch(t, group.key)
			}
		}
		if len(items) == 0 && state.State != "unloaded" && err == "" && !a.tableSectionLoading[key] {
			ui.Text(c, "None").Margin(2, 8, 2, indent).FontSize(10).TextColor(k.muted)
		}
		a.definitionTree(c, k, t, group.key, items, indent)
	}
	if len(m.Clustering) > 0 && a.tableBranch(c, k, t, "clustering", "Clustering", "cluster-key", len(m.Clustering)) {
		a.definitionTree(c, k, t, "clustering", tableDefinitionItems(m, "clustering"), indent)
	}
}

func (a *App) propertyTree(c *ui.Context, k palette, properties []database.Property, scope string, depth int, base float32) {
	if strings.HasPrefix(scope, "inspector:") {
		a.inspectorPropertiesTree(c, k, properties, scope)
		return
	}
	if a.propertyOpen == nil {
		a.propertyOpen = map[string]bool{}
	}
	width := a.explorerWidth
	if strings.HasPrefix(scope, "inspector:") {
		width = 246
		windowWidth, _ := c.Size()
		if windowWidth >= 1100 && a.rightWidth > 0 {
			width = a.rightWidth - 24
		}
	}
	for i, p := range properties {
		key := fmt.Sprintf("%s\x00%d:%s", scope, i, p.Name)
		open := a.propertyOpen[key]
		text := p.Name
		if p.Value != "" {
			text += ": " + p.Value
		}
		row := ui.Row(c.Key(key)).FillWidth().MinWidth(0).Height(26).Padding(0, 6, 0, min(base+float32(depth*14), max(float32(8), width-110))).Gap(5).AlignItems(ui.Center).Tooltip(text)
		if row.Hovered() {
			row.Background(k.border)
		}
		if len(p.Children) > 0 {
			row.Label("Property " + key)
		}
		row.Children(func() {
			if len(p.Children) > 0 {
				if treeChevron(c, k, open, "Toggle property "+key).Clicked() {
					a.propertyOpen[key] = !open
				}
			} else {
				ui.Box(c).Width(12).Shrink(0)
			}
			ui.Text(c, text).SingleLine().MinWidth(0).Grow(1).FontSize(11).TextColor(k.muted)
		})
		if len(p.Children) > 0 && open {
			a.propertyTree(c, k, p.Children, key, depth+1, base)
		}
	}
}

func (a *App) refreshTableBranch(t database.Table, section string) {
	if a.client == nil || a.busy {
		return
	}
	if a.client.Engine() != database.BigQuery || section == "keys" || section == "constraints" || section == "clustering" {
		selected := a.inspectorDefinition
		a.invalidateMetadata()
		a.inspectTable(t, true)
		if selected != nil && selected.Table.ID() == t.ID() {
			a.inspectTableObject(t, selected.Section, selected.Index, selected.Object)
		}
		return
	}
	a.loadTableSection(t, section, true)
}

func (a *App) loadTableSection(t database.Table, section string, force bool) {
	if a.client == nil || a.win == nil || a.busy || a.metadataLoading[t.ID()] {
		return
	}
	if a.tableSectionLoading == nil {
		a.tableSectionLoading = map[string]bool{}
	}
	if a.tableSectionErrors == nil {
		a.tableSectionErrors = map[string]string{}
	}
	key := sectionKey(t, section)
	if a.tableSectionLoading[key] {
		return
	}
	a.tableSectionLoading[key] = true
	delete(a.tableSectionErrors, key)
	client, win, pipeline, version, parent := a.client, a.win, a.introspector, a.metadataVersion, a.connectionContext()
	base := a.metadata[t.ID()]
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		m := base
		var err error
		if pipeline != nil {
			m, err = pipeline.Section(ctx, t, section, force)
		} else {
			err = fmt.Errorf("schema cache is not initialized")
		}
		warning := ""
		if pipeline != nil {
			warning = pipeline.Warning()
		}
		win.Update(func() {
			if a.closed || a.client != client || a.metadataVersion != version {
				return
			}
			delete(a.tableSectionLoading, key)
			a.cacheWarning = warning
			if err != nil {
				a.tableSectionErrors[key] = err.Error()
				return
			}
			current, ok := a.metadata[t.ID()]
			if ok && current.FetchedAt.Equal(m.FetchedAt) {
				// Concurrent branch workers can finish their UI updates out of order.
				// Merge only this branch so a later update cannot erase its siblings.
				switch section {
				case "indexes":
					current.Indexes = m.Indexes
				case "partitions":
					current.Partitions = m.Partitions
				case "policies":
					current.Policies = m.Policies
				case "privileges":
					current.Privileges = m.Privileges
				}
				if current.Sections == nil {
					current.Sections = map[string]database.MetadataSection{}
				}
				current.Sections[section] = m.Sections[section]
				current.FromCache = m.FromCache
				a.metadata[t.ID()] = current
			} else if !ok || !m.FetchedAt.Before(current.FetchedAt) {
				a.metadata[t.ID()] = m
			}
			a.refreshColumnInspection(t, a.metadata[t.ID()])
			a.refreshTableObjectInspection(t, a.metadata[t.ID()])
		})
	}()
}
