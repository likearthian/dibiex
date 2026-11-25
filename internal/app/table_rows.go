package app

import (
	"regexp"
	"strings"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func columnAnnotation(col database.Column) string {
	text := col.Type
	if col.Default != "" && col.Default != "NULL" {
		text += " = " + col.Default
	}
	if col.Identity != "" && col.Default == "" {
		text += " · identity " + strings.ToLower(col.Identity)
	}
	return text
}

func isKeyDefinition(d database.Definition) bool {
	switch strings.ToUpper(strings.TrimSpace(d.Kind)) {
	case "P", "U", "F", "PRIMARY KEY", "FOREIGN KEY", "UNIQUE":
		return true
	}
	return false
}

func uniqueIndex(d database.Definition) bool {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(d.SQL)), "CREATE UNIQUE INDEX ") {
		return true
	}
	for _, p := range d.Properties {
		if (strings.EqualFold(p.Name, "Unique") || p.Name == "indisunique") && (p.Value == "true" || p.Value == "1") {
			return true
		}
	}
	return false
}

// SQL annotations are display hints; the complete native definition remains in
// the tooltip, context menu and expandable metadata. Never execute parsed text.
func firstSQLGroup(sql string) string {
	start, depth := -1, 0
	for i := 0; i < len(sql); i++ {
		b := sql[i]
		if strings.HasPrefix(sql[i:], "--") {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			i += 2
			for i < len(sql) && !strings.HasPrefix(sql[i:], "*/") {
				i++
			}
			i++
			continue
		}
		if b == '\'' || b == '"' || b == '`' || b == '[' {
			close := b
			if b == '[' {
				close = ']'
			}
			for i++; i < len(sql); i++ {
				if sql[i] == '\\' && close == '\'' {
					i++
					continue
				}
				if sql[i] == close {
					if i+1 < len(sql) && sql[i+1] == close {
						i++
						continue
					}
					break
				}
			}
			continue
		}
		if b == '(' {
			if depth == 0 {
				start = i
			}
			depth++
		}
		if b == ')' && depth > 0 {
			depth--
			if depth == 0 {
				return sql[start : i+1]
			}
		}
	}
	return ""
}

var triggerTiming = regexp.MustCompile(`(?is)\b(BEFORE|AFTER|INSTEAD\s+OF)\s+(.+?)\s+ON\b`)
var triggerFunction = regexp.MustCompile(`(?is)\bEXECUTE\s+(?:FUNCTION|PROCEDURE)\s+(.+?)\s*;?$`)

func definitionAnnotation(d database.Definition, section string) string {
	switch section {
	case "keys", "constraints":
		if group := firstSQLGroup(d.SQL); group != "" && group != "(not enforced)" {
			return group
		}
		if isKeyDefinition(d) {
			text := strings.TrimSuffix(d.SQL, " (not enforced)")
			if i := strings.Index(text, " → "); i >= 0 {
				text = text[:i]
			}
			if text != "" {
				return "(" + text + ")"
			}
		}
	case "indexes":
		text := firstSQLGroup(d.SQL)
		if text == "" {
			for _, p := range d.Properties {
				if p.Name != "Columns / expressions" {
					continue
				}
				var columns []string
				for _, term := range p.Children {
					name, key, desc := "", true, false
					for _, field := range term.Children {
						switch field.Name {
						case "name":
							name = field.Value
						case "key":
							key = field.Value == "1"
						case "desc":
							desc = field.Value == "1"
						}
					}
					if !key {
						continue
					}
					if name == "" || name == "NULL" {
						name = "expression"
					}
					if desc {
						name += " DESC"
					}
					columns = append(columns, name)
				}
				if len(columns) > 0 {
					text = "(" + strings.Join(columns, ", ") + ")"
				}
			}
		}
		if uniqueIndex(d) {
			text = strings.TrimSpace(text + " UNIQUE")
		}
		return text
	case "triggers":
		timing := triggerTiming.FindStringSubmatch(d.SQL)
		text := ""
		if len(timing) > 2 {
			text = strings.ToLower(timing[1] + " " + timing[2])
		}
		target := triggerFunction.FindStringSubmatch(d.SQL)
		if len(target) > 1 {
			function := strings.TrimSuffix(strings.TrimSpace(target[1]), "()")
			if text != "" {
				text += " → "
			}
			text += function
		}
		return text
	}
	return ""
}

func (a *App) definitionTree(c *ui.Context, k palette, t database.Table, section string, items []database.Definition, base float32) {
	for i, d := range items {
		name := tableDefinitionName(d, i)
		key := definitionTreeKey(t, section, i, name)
		annotation := definitionAnnotation(d, section)
		row := ui.Row(c.Key(key)).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, min(base+12, max(float32(8), a.explorerWidth-100))).Gap(7).AlignItems(ui.Center).Focusable().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Inspect " + section + " " + name).Tooltip(name + "\n" + annotation + "\n" + d.SQL)
		if a.selectedTree == key {
			row.Background(k.selected).Radius(4)
		} else if row.Hovered() {
			row.Background(k.border)
		}
		row.Children(func() {
			icon, color := "catalog-object", k.muted
			switch section {
			case "keys":
				icon = "key"
				color = ui.Hex("#dfba5e")
				if strings.EqualFold(d.Kind, "f") || strings.EqualFold(d.Kind, "FOREIGN KEY") {
					icon = "foreign-key"
				}
			case "indexes":
				icon = "index-item"
				if uniqueIndex(d) {
					icon = "index-unique"
				}
				color = k.accent
			case "triggers":
				icon = "trigger"
				color = k.accent
			}
			ui.Icon(c, controlIcon(icon)).Size(15, 15).Shrink(0).TextColor(color)
			ui.Text(c, name).SingleLine().MinWidth(0).TextColor(k.text)
			ui.Text(c, annotation).Label("Details for " + section + " " + name).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.muted)
			objectActions(c, k, row, section+" "+name, func(m *ui.Menu) {
				if m.Item("Inspect metadata").Chosen() {
					a.inspectTableObject(t, section, i, d)
				}
				if m.Item("Open definition in SQL editor").Disabled(d.SQL == "").Chosen() {
					a.addQuery(d.SQL)
				}
			})
		})
		if row.Clicked() || row.Shortcut(0, ui.KeyEnter) {
			a.inspectTableObject(t, section, i, d)
		}
	}
}
