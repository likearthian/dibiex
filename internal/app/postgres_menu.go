package app

import (
	"fmt"
	"strings"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) tableQuery(t database.Table, name, sql string, run bool) {
	a.sqlDraft(name+"-"+t.Name, sql)
	if run {
		a.executionLine = 1
		a.runSQL(sql, false)
	}
}

type tableMenuItem struct {
	label, icon string
	disabled    bool
	action      func()
	children    []tableMenuItem
}

func (a *App) postgresTableItems(t database.Table) []tableMenuItem {
	action := func(label, icon, kind string) tableMenuItem {
		return tableMenuItem{label: label, icon: icon, action: func() { a.openTableAction(t, kind) }}
	}
	create := []tableMenuItem{{label: "Table…", icon: "table", action: func() { a.openTableDesigner(t.Schema) }}}
	for _, item := range []struct{ kind, icon string }{{"Column", "type-unknown"}, {"Index", "index"}, {"Trigger", "trigger"}, {"Rule", "sql-file"}, {"Policy", "shield"}} {
		create = append(create, action(item.kind+"…", item.icon, item.kind))
	}
	var constraints, scripts, truncate, data []tableMenuItem
	for _, item := range []struct{ kind, icon string }{{"Primary key", "key"}, {"Foreign key", "foreign-key"}, {"Unique", "index-unique"}, {"Check", "constraint"}} {
		constraints = append(constraints, action(item.kind+"…", item.icon, item.kind))
	}
	create = append(create, tableMenuItem{label: "Constraint", icon: "constraint", children: constraints})
	for _, kind := range []string{"CREATE", "SELECT", "INSERT", "UPDATE", "DELETE"} {
		scripts = append(scripts, tableMenuItem{label: kind + " Script", icon: "sql-file", disabled: kind == "CREATE", action: func() {
			if kind == "CREATE" {
				a.openTableAction(t, "CREATE Script")
			} else {
				a.openTableScript(t, kind)
			}
		}})
	}
	for _, option := range []struct {
		name             string
		restart, cascade bool
	}{
		{"Truncate", false, false}, {"Truncate (Restart Identity)", true, false},
		{"Truncate Cascade", false, true}, {"Truncate Cascade (Restart Identity)", true, true},
	} {
		truncate = append(truncate, tableMenuItem{label: option.name, icon: "trash", action: func() {
			a.openTableAction(t, "Truncate")
			a.tableAction.action.RestartIdentity, a.tableAction.action.Cascade = option.restart, option.cascade
		}})
	}
	for _, kind := range []string{"All Rows", "First 100 Rows", "Last 100 Rows", "Filtered Rows…"} {
		data = append(data, tableMenuItem{label: kind, icon: "table", action: func() { a.openTableData(t, kind) }})
	}
	return []tableMenuItem{
		{label: "Count Rows", icon: "type-number", action: func() {
			a.countTableRows(t)
		}},
		{label: "Create", icon: "plus", children: create},
		action("Edit table…", "settings", "Properties"),
		{},
		action("Drop", "trash", "Drop"),
		{label: "Drop (Cascade)", icon: "trash", action: func() { a.openTableAction(t, "Drop"); a.tableAction.action.Cascade = true }},
		{label: "Refresh…", icon: "refresh", disabled: a.busy, action: func() { a.invalidateMetadata(); a.inspectTable(t, true) }},
		{label: "Restore…", icon: "folder-open", disabled: true, action: func() { a.openTableAction(t, "Restore") }},
		{label: "Backup…", icon: "save", disabled: true, action: func() { a.openTableAction(t, "Backup") }},
		action("Import/Export Data…", "copy", "Import/Export Data"),
		action("Reset Statistics", "refresh", "Reset Statistics"),
		{label: "ERD For Table", icon: "schema", action: func() { a.openTableERD(t) }},
		action("Maintenance…", "settings", "Maintenance"),
		{label: "Scripts", icon: "sql-file", children: scripts},
		{label: "Truncate", icon: "trash", children: truncate},
		{label: "View/Edit Data", icon: "table", children: data},
		action("Search Objects…", "search", "Search Objects"),
		{label: "Query Tool", icon: "console", action: func() { a.tableQuery(t, "query", t.Preview(database.PostgreSQL), false) }},
		action("Properties…", "inspector", "Properties"),
	}
}

func (a *App) postgresTableMenu(m *ui.Menu, t database.Table) {
	var build func(*ui.Menu, []tableMenuItem)
	build = func(m *ui.Menu, items []tableMenuItem) {
		for _, item := range items {
			if item.label == "" {
				m.Separator()
			} else if len(item.children) > 0 {
				m.Submenu(item.label, func(m *ui.Menu) { build(m, item.children) }).Disabled(item.disabled)
			} else if m.Item(item.label).Disabled(item.disabled).Chosen() && item.action != nil {
				item.action()
			}
		}
	}
	build(m, a.postgresTableItems(t))
}

func tableSQLTemplate(t database.Table, kind string, columns []database.Column) (string, error) {
	ref := t.Reference(database.PostgreSQL)
	var names, values, setters []string
	for _, col := range columns {
		name := database.QuoteIdentifier(col.Name)
		if col.Generated == "" && col.Identity == "" {
			names = append(names, name)
			values = append(values, "NULL")
			setters = append(setters, name+" = "+name)
		}
	}
	switch kind {
	case "SELECT":
		return "SELECT * FROM " + ref + " LIMIT 1000;", nil
	case "INSERT":
		if len(names) == 0 {
			return "INSERT INTO " + ref + " DEFAULT VALUES;", nil
		}
		return "-- Replace NULL values before executing. Omitted columns use server defaults.\nINSERT INTO " + ref + " (" + strings.Join(names, ", ") + ")\nVALUES (" + strings.Join(values, ", ") + ");", nil
	case "UPDATE":
		if len(setters) == 0 {
			return "", fmt.Errorf("table has no directly editable columns")
		}
		return "-- Replace the assignments and FALSE with your intended values and row predicate.\nUPDATE " + ref + "\nSET " + strings.Join(setters, ",\n    ") + "\nWHERE FALSE;", nil
	case "DELETE":
		return "-- Replace FALSE with your intended row predicate before executing.\nDELETE FROM " + ref + "\nWHERE FALSE;", nil
	}
	return "", fmt.Errorf("unsupported script type")
}
