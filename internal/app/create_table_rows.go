package app

import (
	"fmt"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

var postgresColumnTypes = []string{
	"smallint", "integer", "bigint", "numeric(18,2)", "real", "double precision",
	"varchar(255)", "char(1)", "text", "boolean", "date", "time", "timestamp",
	"timestamptz", "uuid", "json", "jsonb", "bytea", "text[]", "integer[]",
}

func columnTypeField(c *ui.Context, k palette, col *database.PostgreSQLColumn, index int, disabled ...bool) {
	ui.Row(c.Key("column-type")).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center).Disabled(len(disabled) > 0 && disabled[0]).Children(func() {
		ui.TextInput(c.Key("type"), &col.Type).Grow(1).MinWidth(0).Placeholder("Data type").Label(fmt.Sprintf("Column %d type", index+1)).Tooltip("Choose a type, then edit its length or precision here, e.g. varchar(80) or numeric(12,4). Custom and array types are supported.")
		toolbarAction(c, k, "chevron-down", fmt.Sprintf("Choose column %d data type", index+1), k.muted).Size(26, 26).PopoverMenuButton(func(m *ui.Menu) {
			for _, typ := range postgresColumnTypes {
				if m.Item(typ).Chosen() {
					col.Type = typ
				}
			}
		})
	})
}

func (a *App) tableDesignerColumns(c *ui.Context, k palette, f *tableDesigner) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("Columns · %d", len(f.definition.Columns))).FontWeight(600).TextColor(k.text)
		ui.Spacer(c)
		if ui.Button(c, "+ Add column").Label("Add table column").Clicked() {
			f.addColumn()
		}
	})
	if len(f.definition.Columns) == 0 {
		ui.Text(c, "Add the columns for your new table. Set primary keys here, or define composite keys in Constraints.").TextColor(k.muted).FontSize(12)
	}
	if f.edit != nil {
		ui.Text(c, "Edit existing primary/unique keys in Constraints. Identity and generated-column settings are preserved; removals require confirmation before Save.").FontSize(11).TextColor(k.muted)
	}
	remove := -1
	for i := range f.definition.Columns {
		col := &f.definition.Columns[i]
		original := f.originalColumn(col.Number)
		protected := original != nil && (original.Original.Identity != "" || original.Original.Generated != "")
		ui.Column(c.Key(fmt.Sprintf("column:%d", f.columnIDs[i]))).FillWidth().MinWidth(0).Padding(12).Gap(10).Radius(6).Border(1, k.border).Background(k.panel).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%02d", i+1)).Width(22).FontSize(11).TextColor(k.muted)
				ui.TextInput(c.Key("name"), &col.Name).Grow(1).MinWidth(0).Placeholder("Column name").Label(fmt.Sprintf("Column %d name", i+1))
				columnTypeField(c, k, col, i, protected)
				if toolbarAction(c, k, "x", fmt.Sprintf("Remove column %d", i+1), k.muted).Disabled(protected).Clicked() {
					remove = i
				}
			})
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &col.NotNull, "Not null").Label(fmt.Sprintf("Column %d not null", i+1)).Disabled(protected || original != nil && original.Original.PrimaryKey)
				ui.Checkbox(c, &col.PrimaryKey, "Primary key").Label(fmt.Sprintf("Column %d primary key", i+1)).Disabled(original != nil)
				ui.Checkbox(c, &col.Unique, "Unique").Label(fmt.Sprintf("Column %d unique", i+1)).Disabled(original != nil)
			})
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, "Default SQL expression").FontSize(11).TextColor(k.muted)
					ui.TextInput(c.Key("default"), &col.Default).FillWidth().Disabled(protected).Label(fmt.Sprintf("Column %d default", i+1))
				})
				ui.Column(c).Width(170).Gap(4).Children(func() {
					ui.Text(c, "Identity").FontSize(11).TextColor(k.muted)
					ui.Select(c.Key("identity"), &col.Identity, []string{"None", "ALWAYS", "BY DEFAULT"}).FillWidth().Disabled(original != nil).Label(fmt.Sprintf("Column %d identity", i+1))
				})
			})
			if original != nil && !protected {
				ui.TextInput(c.Key("using"), &col.Using).FillWidth().Placeholder("USING expression for a type change (optional)").Label(fmt.Sprintf("Column %d using", i+1)).Tooltip("Use the original column name in conversion expressions.")
			}
			ui.TextInput(c.Key("comment"), &col.Comment).FillWidth().Placeholder("Column comment (optional)").Label(fmt.Sprintf("Column %d comment", i+1))
			if original != nil && original.Original.Generated != "" {
				ui.Text(c, "Generated expression · "+original.Original.Default).FontSize(11).TextColor(k.muted)
			}
		})
	}
	if remove >= 0 {
		f.definition.Columns = append(f.definition.Columns[:remove], f.definition.Columns[remove+1:]...)
		f.columnIDs = append(f.columnIDs[:remove], f.columnIDs[remove+1:]...)
	}
}

func (a *App) tableDesignerConstraints(c *ui.Context, k palette, f *tableDesigner) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Table constraints").FontWeight(600).TextColor(k.text)
		ui.Spacer(c)
		if ui.Button(c, "+ Add constraint").Label("Add table constraint").Clicked() {
			f.nextID++
			f.constraintIDs = append(f.constraintIDs, f.nextID)
			f.definition.Constraints = append(f.definition.Constraints, database.PostgreSQLConstraint{Kind: "UNIQUE", ReferenceSchema: f.definition.Schema, OnDelete: "NO ACTION"})
		}
	})
	ui.Text(c, "Use comma-separated column names for composite keys. Column primary-key checkboxes form one composite key.").FontSize(12).TextColor(k.muted)
	remove := -1
	for i := range f.definition.Constraints {
		constraint := &f.definition.Constraints[i]
		readonly := constraint.ReadOnlyReason != ""
		ui.Column(c.Key(fmt.Sprintf("constraint:%d", f.constraintIDs[i]))).FillWidth().MinWidth(0).Padding(12).Gap(10).Radius(6).Border(1, k.border).Background(k.panel).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.TextInput(c.Key("name"), &constraint.Name).Grow(1).MinWidth(0).Disabled(readonly).Placeholder("Name (optional)").Label(fmt.Sprintf("Constraint %d name", i+1))
				ui.Select(c.Key("kind"), &constraint.Kind, []string{"PRIMARY KEY", "UNIQUE", "FOREIGN KEY", "CHECK"}).Width(160).Disabled(readonly).Label(fmt.Sprintf("Constraint %d kind", i+1))
				if toolbarAction(c, k, "x", fmt.Sprintf("Remove constraint %d", i+1), k.muted).Clicked() {
					remove = i
				}
			})
			if readonly {
				ui.Text(c, constraint.ReadOnlyReason).FontSize(11).TextColor(k.muted)
				inspectorCode(c, k, "Native constraint SQL", constraint.SQL)
				return
			}
			if constraint.Kind == "CHECK" {
				ui.TextInput(c.Key("check"), &constraint.Expression).FillWidth().Placeholder("SQL expression, e.g. amount >= 0").Label(fmt.Sprintf("Constraint %d expression", i+1))
			} else {
				ui.TextInput(c.Key("columns"), &constraint.Columns).FillWidth().Placeholder("Columns, e.g. tenant_id, id").Label(fmt.Sprintf("Constraint %d columns", i+1))
			}
			if constraint.Kind == "FOREIGN KEY" {
				ui.Row(c).FillWidth().Gap(10).Children(func() {
					ui.TextInput(c.Key("ref-schema"), &constraint.ReferenceSchema).Grow(1).MinWidth(0).Placeholder("Referenced schema").Label(fmt.Sprintf("Constraint %d reference schema", i+1))
					ui.TextInput(c.Key("ref-table"), &constraint.ReferenceTable).Grow(1).MinWidth(0).Placeholder("Referenced table").Label(fmt.Sprintf("Constraint %d reference table", i+1))
				})
				ui.TextInput(c.Key("ref-columns"), &constraint.ReferenceColumns).FillWidth().Placeholder("Referenced columns").Label(fmt.Sprintf("Constraint %d reference columns", i+1))
				ui.Select(c.Key("delete"), &constraint.OnDelete, []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"}).FillWidth().Label(fmt.Sprintf("Constraint %d on delete", i+1))
			}
		})
	}
	if remove >= 0 {
		f.definition.Constraints = append(f.definition.Constraints[:remove], f.definition.Constraints[remove+1:]...)
		f.constraintIDs = append(f.constraintIDs[:remove], f.constraintIDs[remove+1:]...)
	}
}
