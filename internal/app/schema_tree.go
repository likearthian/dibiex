package app

import (
	"fmt"
	"strings"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func objectIcon(kind string) string {
	switch kind {
	case "Views":
		return "view"
	case "Materialized views":
		return "materialized-view"
	case "Partitioned tables":
		return "partitioned-table"
	}
	return "table"
}

func columnTypeIcon(col database.Column) string {
	typ := strings.ToUpper(strings.TrimSpace(col.Type))
	switch {
	case col.Mode == "REPEATED" || strings.HasPrefix(typ, "ARRAY") || strings.HasSuffix(typ, "[]"):
		return "type-array"
	case len(col.Fields) > 0 || strings.Contains(typ, "RECORD") || strings.Contains(typ, "STRUCT") || strings.HasPrefix(typ, "JSON"):
		return "type-record"
	case strings.HasPrefix(typ, "BOOL"):
		return "type-boolean"
	case typ == "DATE":
		return "type-date"
	case strings.Contains(typ, "TIME") || strings.HasPrefix(typ, "INTERVAL"):
		return "type-time"
	case strings.Contains(typ, "CHAR") || typ == "TEXT" || typ == "STRING" || strings.HasPrefix(typ, "CLOB"):
		return "type-text"
	case strings.Contains(typ, "BLOB") || strings.Contains(typ, "BYTE") || strings.Contains(typ, "BINARY"):
		return "type-binary"
	}
	for _, prefix := range []string{"INT", "BIGINT", "SMALLINT", "TINYINT", "MEDIUMINT", "SERIAL", "BIGSERIAL", "SMALLSERIAL", "FLOAT", "DOUBLE", "REAL", "DECIMAL", "NUMERIC", "BIGNUMERIC", "NUMBER", "MONEY"} {
		if strings.HasPrefix(typ, prefix) {
			return "type-number"
		}
	}
	return "type-unknown"
}

func dataTypeIcon(c *ui.Context, k palette, col database.Column) {
	typ := col.Type
	if typ == "" {
		typ = "ANY"
	}
	icon := columnTypeIcon(col)
	ui.Icon(c, controlIcon(icon)).Size(14, 14).Shrink(0).TextColor(k.muted).Label("Data type " + typ + " for " + col.Name).Tooltip(typ)
}

func columnMarkers(c *ui.Context, k palette, col database.Column) {
	if col.PrimaryKey {
		ui.Icon(c, controlIcon("key")).Size(12, 12).TextColor(ui.Hex("#dfba5e")).Tooltip("Primary key")
	}
	if col.ForeignKey {
		ui.Icon(c, controlIcon("foreign-key")).Size(12, 12).TextColor(k.muted).Tooltip("Foreign key")
	}
	if col.Partitioning {
		ui.Icon(c, controlIcon("partition-key")).Size(12, 12).TextColor(k.green).Tooltip("Partitioning column")
	}
	if col.ClusteringOrdinal > 0 {
		ui.Icon(c, controlIcon("cluster-key")).Size(12, 12).TextColor(k.accent).Tooltip(fmt.Sprintf("Clustering column · position %d", col.ClusteringOrdinal))
	}
}

func columnTooltip(col database.Column) string {
	text := columnLabel(col) + "\nNullable: " + col.Nullable + " · Mode: " + col.Mode
	if col.Default != "" {
		text += "\nDefault: " + col.Default
	}
	if col.Generated != "" {
		text += "\nGenerated: " + col.Generated
	}
	if col.Identity != "" {
		text += "\nIdentity: " + col.Identity
	}
	if col.Partitioning {
		text += "\nPartitioning column"
	}
	if col.ClusteringOrdinal > 0 {
		text += fmt.Sprintf("\nClustering position: %d", col.ClusteringOrdinal)
	}
	for _, ref := range col.References {
		text += "\nReferences: " + ref.Project + "." + ref.Schema + "." + ref.Table + "." + ref.Column
	}
	if col.Description != "" {
		text += "\n" + col.Description
	}
	return text
}

func (a *App) columnTree(c *ui.Context, k palette, columns []database.Column, scope, path string, depth int, base float32, table database.Table) {
	if a.fieldOpen == nil {
		a.fieldOpen = map[string]bool{}
	}
	for _, col := range columns {
		key := path + "\x00" + col.Name
		columnPath := strings.Split(strings.TrimPrefix(key, "\x00"), "\x00")
		selectionKey := columnTreeKey(table, columnPath)
		open := a.fieldOpen[scope+key] || a.filter != ""
		available := float32(246)
		if strings.HasPrefix(scope, "explorer:") {
			available = a.explorerWidth
		}
		indent := min(base+float32(depth*14), max(float32(8), available-118))
		row := ui.Row(c.Key(scope+key)).FillWidth().MinWidth(0).Height(27).Padding(0, 6, 0, indent).Gap(4).AlignItems(ui.Center).Focusable().Role(ui.RoleButton).Cursor(ui.CursorPointer).Label("Column " + strings.ReplaceAll(strings.TrimPrefix(key, "\x00"), "\x00", ".")).Tooltip(columnTooltip(col))
		if a.selectedTree == selectionKey {
			row.Background(k.selected).Radius(4)
		} else if row.Hovered() {
			row.Background(k.border)
		}
		toggled := false
		row.Children(func() {
			if len(col.Fields) > 0 {
				icon := "chevron-right"
				if open {
					icon = "chevron-down"
				}
				button := ui.ButtonBase(c.Key("field-chevron:"+scope+key)).Size(12, 18).Shrink(0).Center().Label("Toggle field " + strings.Join(columnPath, "."))
				button.Children(func() { ui.Icon(c, controlIcon(icon)).Size(12, 12).TextColor(k.muted) })
				if button.Clicked() {
					a.fieldOpen[scope+key] = !open
					toggled = true
				}
			} else {
				ui.Box(c).Width(12)
			}
			if col.Partitioning {
				ui.Icon(c, controlIcon("partition-key")).Size(12, 12).TextColor(k.green).Tooltip("Partitioning column")
			}
			if col.ClusteringOrdinal > 0 {
				ui.Icon(c, controlIcon("cluster-key")).Size(12, 12).TextColor(k.accent).Tooltip(fmt.Sprintf("Clustering column · position %d", col.ClusteringOrdinal))
			}
			ui.Box(c).Size(18, 18).Shrink(0).Children(func() {
				dataTypeIcon(c, k, col)
				if col.PrimaryKey || col.ForeignKey {
					icon, color := "key", ui.Hex("#dfba5e")
					if !col.PrimaryKey {
						icon = "foreign-key"
						color = k.muted
					}
					ui.Icon(c, controlIcon(icon)).Absolute().Right(0).Bottom(0).Size(9, 9).TextColor(color)
				}
			})
			ui.Text(c, col.Name).SingleLine().MinWidth(0).FontSize(13).TextColor(k.text)
			ui.Text(c, columnAnnotation(col)).Label("Column details for " + col.Name).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted)
			if path == "" && editablePostgreSQLTable(a.client.Engine(), table) {
				objectActions(c, k, row, "column "+table.Label()+"."+col.Name, func(m *ui.Menu) {
					if m.Item("Edit column…").Disabled(a.client == nil).Chosen() {
						a.openColumnEditor(table, col.Name)
					}
				})
			}
		})
		if (row.Clicked() || row.Shortcut(0, ui.KeyEnter)) && !toggled {
			a.inspectColumn(table, columnPath, col)
		}
		if len(col.Fields) > 0 && open {
			a.columnTree(c, k, col.Fields, scope, key, depth+1, base, table)
		}
	}
}

func columnsMatch(columns []database.Column, filter string) bool {
	for _, col := range columns {
		if strings.Contains(strings.ToLower(col.Name+" "+col.Type), filter) || columnsMatch(col.Fields, filter) {
			return true
		}
	}
	return false
}
