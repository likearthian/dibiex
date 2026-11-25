package app

import (
	"fmt"
	"strings"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

type columnInspection struct {
	Table  database.Table
	Path   []string
	Column database.Column
}

func columnTreeKey(table database.Table, path []string) string {
	return "column:" + table.ID() + "\x00" + strings.Join(path, "\x00")
}

func (a *App) inspectColumn(table database.Table, path []string, col database.Column) {
	a.inspectorColumn = &columnInspection{Table: table, Path: append([]string(nil), path...), Column: col}
	a.inspectorObject = nil
	a.inspectorDefinition = nil
	a.inspectorTable = table
	a.activeTable = table.ID()
	a.openInspector()
	a.selectedTree = columnTreeKey(table, path)
	a.setObjectPath(append(append(a.tableObjectPath(table), "columns"), path...)...)
}

func (a *App) refreshColumnInspection(table database.Table, m database.TableMetadata) {
	selected := a.inspectorColumn
	if selected == nil || selected.Table.ID() != table.ID() {
		return
	}
	if m.Kind != "" {
		table.Kind = m.Kind
	}
	columns := m.Columns
	for i, name := range selected.Path {
		found := false
		for _, col := range columns {
			if col.Name != name {
				continue
			}
			found = true
			if i == len(selected.Path)-1 {
				selected.Column = col
				selected.Table = table
				if a.selectedTree == columnTreeKey(table, selected.Path) {
					a.setObjectPath(append(append(a.tableObjectPath(table), "columns"), selected.Path...)...)
				}
				return
			}
			columns = col.Fields
			break
		}
		if !found {
			a.inspectorColumn = nil
			a.selectedTree = "table:" + table.ID()
			a.setObjectPath(a.tableObjectPath(table)...)
			a.status = "Column no longer listed in refreshed metadata"
			return
		}
	}
}

func (a *App) columnInspector(c *ui.Context, k palette) {
	selected := a.inspectorColumn
	col := selected.Column
	table := selected.Table
	name := strings.Join(selected.Path, ".")
	if ui.Button(c, "Back to table").Margin(8, 12, 0, 12).Clicked() {
		a.selectedTree = "table:" + table.ID()
		a.inspectTable(table)
		return
	}
	inspectorHeading(c, k, col.Name, "Column", inspectorContext(table.Label(), col.Type), columnTypeIcon(col), "Inspected column "+name, func() {
		if len(selected.Path) == 1 && editablePostgreSQLTable(a.client.Engine(), table) {
			if toolbarAction(c, k, "settings", "Edit column", k.muted).Disabled(a.client == nil).Clicked() {
				a.openColumnEditor(table, col.Name)
			}
		}
		if toolbarAction(c, k, "refresh", "Refresh column metadata", k.muted).Disabled(a.client == nil || a.metadataLoading[table.ID()]).Clicked() {
			a.inspectTable(table, true)
			a.inspectorColumn = selected
			a.selectedTree = columnTreeKey(table, selected.Path)
			a.setObjectPath(append(append(a.tableObjectPath(table), "columns"), selected.Path...)...)
		}
	})
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Padding(12).Gap(8).Children(func() {
			if err := a.metadataErrors[table.ID()]; err != "" {
				ui.Text(c, err).TextColor(k.danger)
			}
			if a.metadataLoading[table.ID()] {
				ui.Text(c, "Refreshing metadata…").FontSize(11).TextColor(k.muted)
			}
			if m, ok := a.metadata[table.ID()]; ok && !m.FetchedAt.IsZero() {
				ui.Text(c, "Metadata · "+m.FetchedAt.Local().Format("02 Jan 15:04")).FontSize(10).TextColor(k.muted)
			}
			yesNo := func(value bool) string {
				if value {
					return "Yes"
				}
				return "No"
			}
			defaultValue := col.Default
			if defaultValue == "" {
				defaultValue = "None"
			}
			fields := []struct{ label, value string }{{"Name", col.Name}, {"Data type", col.Type}, {"Nullable", col.Nullable}, {"Default", defaultValue}, {"Mode", col.Mode}, {"Primary key", yesNo(col.PrimaryKey)}, {"Foreign key", yesNo(col.ForeignKey)}, {"Generated", col.Generated}, {"Identity", col.Identity}, {"Collation", col.Collation}}
			if col.NativeType != "" && col.NativeType != col.Type {
				fields = append(fields, struct{ label, value string }{"Native type", col.NativeType})
			}
			if col.Ordinal > 0 {
				fields = append(fields, struct{ label, value string }{"Position", fmt.Sprint(col.Ordinal)})
			}
			if col.Partitioning {
				fields = append(fields, struct{ label, value string }{"Partitioning", "Yes"})
			}
			if col.ClusteringOrdinal > 0 {
				fields = append(fields, struct{ label, value string }{"Clustering", fmt.Sprint(col.ClusteringOrdinal)})
			}
			for _, field := range fields {
				if field.value == "" {
					continue
				}
				if field.label == "Default" && col.Default != "" {
					inspectorCode(c, k, "Default", col.Default)
				} else {
					inspectorField(c, k, field.label, field.value)
				}
			}
			if col.Description != "" {
				inspectorSection(c, k, "Description")
				ui.Text(c, col.Description).Selectable().TextColor(k.text)
			}
			for _, ref := range col.References {
				var parts []string
				for _, part := range []string{ref.Project, ref.Schema, ref.Table, ref.Column} {
					if part != "" {
						parts = append(parts, part)
					}
				}
				ui.Text(c, "References: "+strings.Join(parts, ".")).Selectable().TextColor(k.muted)
			}
			if len(col.Properties) > 0 {
				a.propertyTree(c, k, []database.Property{{Name: "Native metadata", Children: col.Properties}}, "inspector:column:"+columnTreeKey(table, selected.Path), 0, 0)
			}
			if len(col.Fields) > 0 {
				inspectorSection(c, k, "Fields")
				a.columnTree(c, k, col.Fields, "inspector:"+table.ID(), "\x00"+strings.Join(selected.Path, "\x00"), 0, 0, table)
			}
		})
	})
}
