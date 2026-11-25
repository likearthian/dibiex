package app

import (
	"fmt"
	"strings"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type tableObjectInspection struct {
	Table         database.Table
	Section, Name string
	Index         int
	Object        database.Definition
}

func tableDefinitionName(d database.Definition, index int) string {
	if d.Name != "" {
		return d.Name
	}
	return fmt.Sprintf("%s %d", strings.ToLower(d.Kind), index+1)
}
func definitionTreeKey(t database.Table, section string, index int, name string) string {
	return fmt.Sprintf("definition:%s:%s:%d:%s", t.ID(), section, index, name)
}
func tableDefinitionItems(m database.TableMetadata, section string) []database.Definition {
	if section == "clustering" {
		var items []database.Definition
		for i, name := range m.Clustering {
			d := database.Definition{Name: name, Kind: "CLUSTERING COLUMN", Properties: []database.Property{{Name: "Position", Value: fmt.Sprint(i + 1)}}}
			for _, col := range m.Columns {
				if col.Name == name {
					d.Properties = append(d.Properties, database.Property{Name: "Data type", Value: col.Type}, database.Property{Name: "Column metadata", Children: col.Properties})
					break
				}
			}
			items = append(items, d)
		}
		return items
	}
	if section != "keys" && section != "constraints" {
		return m.SectionItems(section)
	}
	var items []database.Definition
	for _, d := range m.Constraints {
		if isKeyDefinition(d) == (section == "keys") {
			items = append(items, d)
		}
	}
	return items
}
func (a *App) inspectTableObject(t database.Table, section string, index int, d database.Definition) {
	name := tableDefinitionName(d, index)
	a.inspectorDefinition = &tableObjectInspection{Table: t, Section: section, Index: index, Name: name, Object: d}
	a.inspectorColumn = nil
	a.inspectorObject = nil
	a.inspectorTable = t
	a.activeTable = t.ID()
	a.openInspector()
	a.selectedTree = definitionTreeKey(t, section, index, name)
	a.setObjectPath(append(a.tableObjectPath(t), section, name)...)
}
func (a *App) refreshTableObjectInspection(t database.Table, m database.TableMetadata) {
	selected := a.inspectorDefinition
	if selected == nil || selected.Table.ID() != t.ID() {
		return
	}
	wasSelected := a.selectedTree == definitionTreeKey(selected.Table, selected.Section, selected.Index, selected.Name)
	items := tableDefinitionItems(m, selected.Section)
	for i, d := range items {
		if tableDefinitionName(d, i) != selected.Name {
			continue
		}
		selected.Object = d
		selected.Index = i
		if m.Kind != "" {
			t.Kind = m.Kind
		}
		selected.Table = t
		if wasSelected {
			a.selectedTree = definitionTreeKey(t, selected.Section, i, selected.Name)
			a.setObjectPath(append(a.tableObjectPath(t), selected.Section, selected.Name)...)
		}
		return
	}
	stateKey := selected.Section
	if stateKey == "keys" {
		stateKey = "constraints"
	}
	if state := m.Sections[stateKey]; state.State == "unavailable" || state.State == "partial" || state.State == "unloaded" {
		return
	}
	a.inspectorDefinition = nil
	if wasSelected {
		a.selectedTree = "table:" + t.ID()
		a.setObjectPath(a.tableObjectPath(t)...)
	}
	a.status = "Object no longer listed in refreshed table metadata"
}
func tableObjectKind(section string, d database.Definition) string {
	switch strings.ToUpper(d.Kind) {
	case "P":
		return "Primary key"
	case "U":
		return "Unique key"
	case "F":
		return "Foreign key"
	case "C":
		return "Check constraint"
	case "X":
		return "Exclusion constraint"
	case "N":
		return "Not-null constraint"
	}
	if d.Kind != "" {
		kind := strings.ToLower(d.Kind)
		return strings.ToUpper(kind[:1]) + kind[1:]
	}
	return section
}
func (a *App) tableObjectInspector(c *ui.Context, k palette) {
	selected := a.inspectorDefinition
	t := selected.Table
	d := selected.Object
	icon := "catalog-object"
	switch selected.Section {
	case "keys":
		icon = "key"
	case "indexes":
		icon = "index-item"
	case "triggers":
		icon = "trigger"
	case "partitions":
		icon = "partitioned-table"
	}
	inspectorHeading(c, k, selected.Name, tableObjectKind(selected.Section, d), t.Label(), icon, "Inspected table object "+selected.Name, func() {
		if toolbarAction(c, k, "refresh", "Refresh object metadata", k.muted).Disabled(a.client == nil || a.busy || a.metadataLoading[t.ID()]).Clicked() {
			a.refreshTableBranch(t, selected.Section)
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Padding(12).Gap(10).Children(func() {
			stateKey := selected.Section
			if stateKey == "keys" {
				stateKey = "constraints"
			}
			m := a.metadata[t.ID()]
			state := m.Sections[stateKey]
			if err := a.metadataErrors[t.ID()]; err != "" {
				ui.Text(c, err).TextColor(k.danger)
			}
			if err := a.tableSectionErrors[sectionKey(t, selected.Section)]; err != "" {
				ui.Text(c, err).TextColor(k.danger)
			}
			if state.Message != "" && (state.State == "unavailable" || state.State == "partial") {
				ui.Text(c, state.Message).TextColor(k.danger)
			}
			if a.metadataLoading[t.ID()] || a.tableSectionLoading[sectionKey(t, selected.Section)] {
				ui.Text(c, "Refreshing metadata…").FontSize(11).TextColor(k.muted)
			}
			fetched := m.FetchedAt
			if state.FetchedAt.After(fetched) {
				fetched = state.FetchedAt
			}
			if !fetched.IsZero() {
				ui.Text(c, "Metadata · "+fetched.Local().Format("02 Jan 15:04")).FontSize(10).TextColor(k.muted)
			}
			if summary := definitionAnnotation(d, selected.Section); summary != "" {
				inspectorField(c, k, "Summary", summary)
			}
			if d.SQL != "" {
				inspectorCode(c, k, "Definition", d.SQL)
				if ui.Button(c, "Open definition in SQL editor").Clicked() {
					a.addQuery(d.SQL)
				}
			}
			if len(d.Properties) > 0 {
				a.propertyTree(c, k, d.Properties, "inspector:table-object:"+definitionTreeKey(t, selected.Section, selected.Index, selected.Name), 0, 0)
			}
		})
	})
}
