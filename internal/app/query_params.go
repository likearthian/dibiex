package app

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

type paramOccurrence struct {
	raw   string // e.g. ":region", "$1", "?", "@circle"
	key   string // identifier used to map to unique queryParamDef
	start int
	end   int
}

type queryParamDef struct {
	Key  string // e.g. ":region", "$1", "?1", "@circle"
	Name string // e.g. "region", "1", "1", "circle"
	Raw  string // e.g. ":region", "$1", "?1", "@circle"
}

type queryParamValue struct {
	Key    string
	Name   string
	Raw    string
	Value  string
	Type   string // "Auto", "String", "Number", "Boolean", "Raw"
	IsNull bool
}

type queryParamsDialog struct {
	tab       *queryTab
	query     string
	write     bool
	action    string // "run", "explain", "export"
	defs      []queryParamDef
	occs      []paramOccurrence
	params    []queryParamValue
	errorText string
}

func extractParameters(sql string, engine database.Engine) ([]paramOccurrence, []queryParamDef) {
	var occurrences []paramOccurrence
	var unique []queryParamDef
	seen := make(map[string]int)
	anonIndex := 0

	for i := 0; i < len(sql); {
		if sqlLineComment(sql, i, engine) {
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				break
			}
			i += j + 1
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			i += 2
			depth := 1
			for i < len(sql) && depth > 0 {
				if strings.HasPrefix(sql[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(sql[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}
		if sql[i] == '$' && engine == database.PostgreSQL {
			j := i + 1
			for j < len(sql) && (unicode.IsLetter(rune(sql[j])) || sql[j] == '_' || (j > i+1 && sql[j] >= '0' && sql[j] <= '9')) {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					break
				}
				i = j + 1 + end + len(tag)
				continue
			}
		}
		q := sql[i]
		if q == '\'' || q == '"' || q == '`' || (q == '[' && engine == database.SQLite) {
			escaped := engine == database.BigQuery || engine == database.MySQL || (engine == database.PostgreSQL && q == '\'' && i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E') && (i < 2 || !unicode.IsLetter(rune(sql[i-2]))))
			close := string(q)
			if q == '[' {
				close = "]"
			}
			if engine == database.BigQuery && strings.HasPrefix(sql[i:], strings.Repeat(string(q), 3)) {
				close = strings.Repeat(string(q), 3)
			}
			i += len(close)
			for i < len(sql) {
				if sql[i] == '\\' && escaped {
					i = min(i+2, len(sql))
					continue
				}
				if strings.HasPrefix(sql[i:], close) {
					i += len(close)
					if len(close) == 1 && i < len(sql) && sql[i] == q && q != '[' {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}

		prevIsIdent := i > 0 && (unicode.IsLetter(rune(sql[i-1])) || unicode.IsDigit(rune(sql[i-1])) || sql[i-1] == '_')

		if sql[i] == ':' && !prevIsIdent {
			if (i > 0 && sql[i-1] == ':') || (i+1 < len(sql) && (sql[i+1] == ':' || sql[i+1] == '=')) {
				for i < len(sql) && sql[i] == ':' {
					i++
				}
				if i < len(sql) && sql[i] == '=' {
					i++
				}
				continue
			}
			if i+1 < len(sql) && (unicode.IsLetter(rune(sql[i+1])) || sql[i+1] == '_') {
				j := i + 1
				for j < len(sql) && (unicode.IsLetter(rune(sql[j])) || unicode.IsDigit(rune(sql[j])) || sql[j] == '_') {
					j++
				}
				raw := sql[i:j]
				key := raw
				name := raw[1:]
				if _, ok := seen[key]; !ok {
					seen[key] = len(unique)
					unique = append(unique, queryParamDef{Key: key, Name: name, Raw: raw})
				}
				occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: j})
				i = j
				continue
			}
		}

		if sql[i] == '@' && !prevIsIdent {
			if (i > 0 && sql[i-1] == '@') || (i+1 < len(sql) && sql[i+1] == '@') {
				for i < len(sql) && sql[i] == '@' {
					i++
				}
				continue
			}
			if i+1 < len(sql) && (unicode.IsLetter(rune(sql[i+1])) || sql[i+1] == '_') {
				j := i + 1
				for j < len(sql) && (unicode.IsLetter(rune(sql[j])) || unicode.IsDigit(rune(sql[j])) || sql[j] == '_') {
					j++
				}
				raw := sql[i:j]
				key := raw
				name := raw[1:]
				if _, ok := seen[key]; !ok {
					seen[key] = len(unique)
					unique = append(unique, queryParamDef{Key: key, Name: name, Raw: raw})
				}
				occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: j})
				i = j
				continue
			}
		}

		if sql[i] == '$' && !prevIsIdent {
			if i+1 < len(sql) {
				if sql[i+1] >= '1' && sql[i+1] <= '9' {
					j := i + 1
					for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
						j++
					}
					raw := sql[i:j]
					key := raw
					name := raw[1:]
					if _, ok := seen[key]; !ok {
						seen[key] = len(unique)
						unique = append(unique, queryParamDef{Key: key, Name: name, Raw: raw})
					}
					occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: j})
					i = j
					continue
				} else if unicode.IsLetter(rune(sql[i+1])) || sql[i+1] == '_' {
					j := i + 1
					for j < len(sql) && (unicode.IsLetter(rune(sql[j])) || unicode.IsDigit(rune(sql[j])) || sql[j] == '_') {
						j++
					}
					raw := sql[i:j]
					key := raw
					name := raw[1:]
					if _, ok := seen[key]; !ok {
						seen[key] = len(unique)
						unique = append(unique, queryParamDef{Key: key, Name: name, Raw: raw})
					}
					occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: j})
					i = j
					continue
				}
			}
		}

		if sql[i] == '?' && !prevIsIdent {
			if i+1 < len(sql) && (sql[i+1] == '|' || sql[i+1] == '&') {
				i += 2
				continue
			}
			if i+1 < len(sql) && sql[i+1] >= '0' && sql[i+1] <= '9' {
				j := i + 1
				for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
					j++
				}
				raw := sql[i:j]
				key := raw
				name := raw[1:]
				if _, ok := seen[key]; !ok {
					seen[key] = len(unique)
					unique = append(unique, queryParamDef{Key: key, Name: name, Raw: raw})
				}
				occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: j})
				i = j
				continue
			}
			anonIndex++
			raw := "?"
			key := fmt.Sprintf("?%d", anonIndex)
			name := fmt.Sprintf("%d", anonIndex)
			unique = append(unique, queryParamDef{Key: key, Name: name, Raw: key})
			occurrences = append(occurrences, paramOccurrence{raw: raw, key: key, start: i, end: i + 1})
			i++
			continue
		}

		i++
	}
	return occurrences, unique
}

func isNumberLiteral(s string) bool {
	if s == "" {
		return false
	}
	trimmed := s
	if trimmed[0] == '+' || trimmed[0] == '-' {
		trimmed = trimmed[1:]
	}
	if trimmed == "" || trimmed[0] < '0' || trimmed[0] > '9' {
		return false
	}
	if len(trimmed) > 1 && trimmed[0] == '0' && trimmed[1] >= '0' && trimmed[1] <= '9' {
		return false
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "eEiInN") {
		return true
	}
	return false
}

func quoteSQLString(s string, engine database.Engine) string {
	escaped := strings.ReplaceAll(s, "'", "''")
	if engine == database.MySQL {
		escaped = strings.ReplaceAll(escaped, `\`, `\\`)
	}
	return "'" + escaped + "'"
}

func formatParamValue(val queryParamValue, engine database.Engine) (string, error) {
	if val.IsNull || val.Type == "NULL" {
		return "NULL", nil
	}
	switch val.Type {
	case "Raw":
		if strings.TrimSpace(val.Value) == "" {
			return "NULL", nil
		}
		return val.Value, nil
	case "Number":
		s := strings.TrimSpace(val.Value)
		if s == "" {
			return "NULL", nil
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return "", fmt.Errorf("parameter %s: invalid number %q", val.Raw, val.Value)
		}
		return s, nil
	case "Boolean":
		s := strings.TrimSpace(strings.ToLower(val.Value))
		if s == "true" || s == "t" || s == "1" || s == "yes" {
			return "TRUE", nil
		}
		if s == "false" || s == "f" || s == "0" || s == "no" {
			return "FALSE", nil
		}
		if s == "" {
			return "NULL", nil
		}
		return "", fmt.Errorf("parameter %s: invalid boolean %q (use true or false)", val.Raw, val.Value)
	case "String":
		return quoteSQLString(val.Value, engine), nil
	default: // "Auto"
		s := strings.TrimSpace(val.Value)
		if strings.EqualFold(s, "null") {
			return "NULL", nil
		}
		if strings.EqualFold(s, "true") {
			return "TRUE", nil
		}
		if strings.EqualFold(s, "false") {
			return "FALSE", nil
		}
		if isNumberLiteral(s) {
			return s, nil
		}
		return quoteSQLString(val.Value, engine), nil
	}
}

func substituteParameters(sql string, occurrences []paramOccurrence, values map[string]string) string {
	if len(occurrences) == 0 {
		return sql
	}
	var sb strings.Builder
	last := 0
	for _, occ := range occurrences {
		sb.WriteString(sql[last:occ.start])
		val, ok := values[occ.key]
		if !ok {
			val = "NULL"
		}
		sb.WriteString(val)
		last = occ.end
	}
	sb.WriteString(sql[last:])
	return sb.String()
}

func (a *App) runAction(query string, write bool, action string) {
	tab := a.currentEditor()
	if tab == nil || tab.tableView != nil || a.queryBusy() || a.client == nil || strings.TrimSpace(query) == "" {
		return
	}
	engine := a.client.Engine()
	occs, defs := extractParameters(query, engine)
	if len(defs) == 0 {
		switch action {
		case "run":
			a.runSQL(query, write)
		case "explain":
			a.runExplain(query)
		case "export":
			a.openQueryExportWithSQL(query)
		}
		return
	}
	a.openQueryParams(tab, query, write, action, defs, occs)
}

func (a *App) openQueryParams(tab *queryTab, query string, write bool, action string, defs []queryParamDef, occs []paramOccurrence) {
	if tab.paramValues == nil {
		tab.paramValues = make(map[string]queryParamValue)
	}
	params := make([]queryParamValue, len(defs))
	for i, def := range defs {
		saved, ok := tab.paramValues[def.Key]
		if !ok {
			saved, ok = tab.paramValues[def.Name]
		}
		if ok {
			params[i] = queryParamValue{
				Key:    def.Key,
				Name:   def.Name,
				Raw:    def.Raw,
				Value:  saved.Value,
				Type:   saved.Type,
				IsNull: saved.IsNull,
			}
		} else {
			params[i] = queryParamValue{
				Key:    def.Key,
				Name:   def.Name,
				Raw:    def.Raw,
				Value:  "",
				Type:   "Auto",
				IsNull: false,
			}
		}
		if params[i].Type == "" {
			params[i].Type = "Auto"
		}
	}
	a.queryParams = &queryParamsDialog{
		tab:    tab,
		query:  query,
		write:  write,
		action: action,
		defs:   defs,
		occs:   occs,
		params: params,
	}
	a.showQueryParams = true
}

func (a *App) openQueryParamsDialog() {
	tab := a.currentEditor()
	if tab == nil || a.client == nil {
		return
	}
	query := strings.TrimSpace(a.querySQL())
	if query == "" {
		a.status = "Enter a SQL query with parameters (e.g. :name, @name, $1, ?)"
		return
	}
	engine := a.client.Engine()
	occs, defs := extractParameters(query, engine)
	if len(defs) == 0 {
		a.status = "No parameters found in active query (use :name, @name, $1, or ?)"
		return
	}
	a.openQueryParams(tab, query, false, "run", defs, occs)
}

func (a *App) submitQueryParams() {
	p := a.queryParams
	if p == nil || p.tab == nil {
		a.showQueryParams = false
		return
	}
	engine := database.SQLite
	if a.client != nil {
		engine = a.client.Engine()
	}

	formatted := make(map[string]string)
	for i := range p.params {
		param := &p.params[i]
		valStr, err := formatParamValue(*param, engine)
		if err != nil {
			p.errorText = err.Error()
			return
		}
		formatted[param.Key] = valStr

		if p.tab.paramValues == nil {
			p.tab.paramValues = make(map[string]queryParamValue)
		}
		p.tab.paramValues[param.Key] = *param
		p.tab.paramValues[param.Name] = *param
	}

	substituted := substituteParameters(p.query, p.occs, formatted)
	action := p.action
	write := p.write

	a.showQueryParams = false
	p.errorText = ""

	switch action {
	case "run":
		a.runSQL(substituted, write)
	case "explain":
		a.runExplain(substituted)
	case "export":
		a.openQueryExportWithSQL(substituted)
	}
}

func (a *App) queryParamsModal(c *ui.Context, k palette) {
	if !a.showQueryParams || a.queryParams == nil {
		return
	}
	p := a.queryParams
	ui.Modal(c.Key("query-parameters-dialog"), &a.showQueryParams, func() {
		ui.Column(c).Width(480).Gap(14).Label("Query parameters dialog").Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, controlIcon("play")).Size(16, 16).TextColor(k.accent)
				ui.Text(c, "Query Parameters").FontSize(17).FontWeight(600).TextColor(k.text)
				ui.Spacer(c)
				countText := fmt.Sprintf("%d parameter", len(p.params))
				if len(p.params) != 1 {
					countText += "s"
				}
				ui.Text(c, countText).FontSize(11).TextColor(k.muted)
			})

			ui.Text(c, "Set parameter values before executing the statement. Values are remembered for this tab.").FontSize(11).TextColor(k.muted)

			ui.Scroll(c).FillWidth().MaxHeight(340).Children(func() {
				ui.Column(c).FillWidth().Gap(12).Padding(2).Children(func() {
					for i := range p.params {
						param := &p.params[i]
						ui.Column(c.Key("param-item-" + param.Key)).FillWidth().Gap(5).Children(func() {
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, param.Raw).Font("JetBrains Mono").FontSize(12).FontWeight(600).TextColor(k.text).Label("Parameter " + param.Raw)
								ui.Spacer(c)
								ui.Text(c, "Type").FontSize(11).TextColor(k.muted)
								ui.Select(c.Key("type-"+param.Key), &param.Type, []string{"Auto", "String", "Number", "Boolean", "Raw"}).Width(88).Disabled(param.IsNull).Label("Type for " + param.Raw)
								ui.Checkbox(c, &param.IsNull, "NULL").Label("NULL for " + param.Raw)
							})
							placeholder := "Value"
							if param.Type == "Number" {
								placeholder = "Number (e.g. 42)"
							} else if param.Type == "Boolean" {
								placeholder = "true or false"
							} else if param.Type == "Raw" {
								placeholder = "SQL expression (e.g. CURRENT_DATE)"
							}
							valInput := ui.TextInput(c.Key("val-"+param.Key), &param.Value).FillWidth().Placeholder(placeholder).Disabled(param.IsNull).Label("Value for " + param.Raw)
							if valInput.Submitted() {
								a.submitQueryParams()
							}
						})
					}
				})
			})

			if p.errorText != "" {
				ui.Text(c, p.errorText).FontSize(11).TextColor(k.danger).Label("Parameter validation error")
			}

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Label("Cancel query parameters").Clicked() {
					a.showQueryParams = false
				}
				actionLabel := "Execute"
				if p.action == "explain" {
					actionLabel = "Explain"
				} else if p.action == "export" {
					actionLabel = "Export"
				}
				if ui.PrimaryButton(c, actionLabel).Label("Execute with parameters").Clicked() {
					a.submitQueryParams()
				}
			})
		})
	})
}
