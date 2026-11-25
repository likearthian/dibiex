package app

import (
	"fmt"
	"strings"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type sqlFunctionSignature struct {
	name        string
	params      []string
	description string
	separator   string
	variadic    bool
}

func (s sqlFunctionSignature) label() string {
	separator := s.separator
	if separator == "" {
		separator = ", "
	}
	return s.name + "(" + strings.Join(s.params, separator) + ")"
}

// Curated common forms, not a server-discovered overload catalog. Sources:
// https://www.postgresql.org/docs/current/functions-string.html
// https://www.postgresql.org/docs/current/functions-aggregate.html
// https://www.sqlite.org/lang_corefunc.html
// https://www.sqlite.org/lang_aggfunc.html
// https://docs.cloud.google.com/bigquery/docs/reference/standard-sql/string_functions
// https://docs.cloud.google.com/bigquery/docs/reference/standard-sql/aggregate_functions
// https://docs.cloud.google.com/bigquery/docs/reference/standard-sql/conditional_expressions
// https://dev.mysql.com/doc/refman/8.4/en/string-functions.html
// https://dev.mysql.com/doc/refman/8.4/en/date-and-time-functions.html
// https://dev.mysql.com/doc/refman/8.4/en/json-functions.html
var sqlFunctionCatalog = func() map[database.Engine]map[string]sqlFunctionSignature {
	catalog := map[database.Engine]map[string]sqlFunctionSignature{}
	for _, engine := range []database.Engine{database.SQLite, database.PostgreSQL, database.MySQL, database.BigQuery} {
		functions := map[string]sqlFunctionSignature{}
		add := func(name, params, description string) {
			var list []string
			if params != "" {
				list = strings.Split(params, "|")
			}
			functions[name] = sqlFunctionSignature{name: name, params: list, description: description}
		}
		add("COUNT", "expression or *", "Counts rows or non-NULL expressions.")
		add("SUM", "expression", "Adds non-NULL values.")
		add("AVG", "expression", "Averages non-NULL values.")
		add("MIN", "expression", "Returns the smallest non-NULL value.")
		add("MAX", "expression", "Returns the largest non-NULL value.")
		add("COALESCE", "value|...", "Returns the first non-NULL value.")
		add("NULLIF", "value1|value2", "Returns NULL when the values are equal.")
		add("LOWER", "string", "Converts text to lowercase.")
		add("UPPER", "string", "Converts text to uppercase.")
		add("LENGTH", "value", "Returns the length of the value.")
		add("SUBSTR", "value|start|[length]", "Returns part of a string; positions start at 1.")
		add("REPLACE", "value|search|replacement", "Replaces matching substrings.")
		add("TRIM", "value|[characters]", "Trims characters from both ends.")
		add("CAST", "expression|type", "Converts an expression to a type.")
		cast := functions["CAST"]
		cast.separator = " AS "
		functions["CAST"] = cast
		if engine == database.SQLite {
			add("IFNULL", "value|fallback", "Returns the fallback when value is NULL.")
			add("GROUP_CONCAT", "expression|[separator]", "Concatenates non-NULL values; the default separator is a comma.")
			add("TOTAL", "expression", "Adds non-NULL values, returning 0.0 for no input.")
			add("JSON_EXTRACT", "json|path|...", "Extracts values at JSON paths.")
		}
		if engine == database.PostgreSQL {
			add("STRING_AGG", "value|delimiter", "Concatenates non-NULL input values with a delimiter.")
			add("ARRAY_AGG", "expression", "Collects input values into an array.")
			add("JSONB_AGG", "expression", "Collects input values into a JSONB array.")
			add("JSONB_BUILD_OBJECT", "key|value|...", "Builds a JSONB object from alternating keys and values.")
		}
		if engine == database.MySQL {
			add("IFNULL", "value|fallback", "Returns the fallback when value is NULL.")
			add("IF", "condition|true_result|else_result", "Chooses a result based on a condition.")
			add("CONCAT", "value|...", "Concatenates values; a NULL argument makes the result NULL.")
			add("CONCAT_WS", "separator|value|...", "Concatenates values with a separator, skipping NULL values.")
			add("GROUP_CONCAT", "expression|[separator]", "Concatenates non-NULL values with a comma by default.")
			add("DATE_FORMAT", "date|format", "Formats a date with MySQL format specifiers.")
			add("STR_TO_DATE", "string|format", "Parses a date from a string using MySQL format specifiers.")
			add("NOW", "", "Returns the current date and time.")
			add("CURDATE", "", "Returns the current date.")
			add("JSON_EXTRACT", "json|path|...", "Extracts values at JSON paths.")
			add("JSON_UNQUOTE", "json_value", "Unquotes a JSON string value.")
			add("REGEXP_LIKE", "value|regexp|[match_type]", "Tests whether a regular expression matches the value.")
		}
		if engine == database.BigQuery {
			add("IF", "condition|true_result|else_result", "Chooses a result based on a condition.")
			add("IFNULL", "value|fallback", "Returns the fallback when value is NULL.")
			add("COUNTIF", "condition", "Counts TRUE conditions.")
			add("STRING_AGG", "expression|[delimiter]", "Concatenates non-NULL values; the default delimiter is a comma.")
			add("ARRAY_AGG", "expression", "Collects input values into an array.")
			add("REGEXP_CONTAINS", "value|regexp", "Tests whether a regular expression matches any part of the value.")
			add("SAFE_CAST", "expression|type", "Converts a value, returning NULL for runtime conversion errors.")
			safe := functions["SAFE_CAST"]
			safe.separator = " AS "
			functions["SAFE_CAST"] = safe
		}
		for name, signature := range functions {
			if len(signature.params) > 0 && signature.params[len(signature.params)-1] == "..." {
				signature.variadic = true
				functions[name] = signature
			}
		}
		catalog[engine] = functions
	}
	return catalog
}()

type sqlSignatureHelp struct {
	signature sqlFunctionSignature
	argument  int
	parameter int
}

func functionArgumentHelp(sql string, offset int, engine database.Engine) *sqlSignatureHelp {
	if len(sql) > completionSQLLimit || offset < 0 || offset > len(sql) {
		return nil
	}
	lo, _ := statementRange(sql, offset, engine)
	tokens, _ := completionTokens(sql[lo:offset], engine)
	if len(tokens) > 32768 {
		return nil
	}
	type call struct {
		name     string
		argument int
		bracket  string
	}
	var stack []call
	for i, t := range tokens {
		switch t.text {
		case "(", "[":
			name := ""
			if t.text == "(" && i > 0 && tokens[i-1].identifier && !tokens[i-1].quoted && (i < 2 || tokens[i-2].text != ".") {
				name = strings.ToUpper(tokens[i-1].text)
			}
			stack = append(stack, call{name: name, bracket: t.text})
		case ")", "]":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			if len(stack) == 0 {
				continue
			}
			current := &stack[len(stack)-1]
			if t.text == "," || t.keyword("AS") && (current.name == "CAST" || current.name == "SAFE_CAST") {
				current.argument++
			}
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].name == "" {
			continue
		}
		signature, ok := sqlFunctionCatalog[engine][stack[i].name]
		if !ok {
			return nil
		}
		argument := stack[i].argument
		parameter := argument
		if signature.variadic {
			parameter = min(argument, len(signature.params)-1)
		}
		if parameter >= len(signature.params) {
			return nil
		}
		return &sqlSignatureHelp{signature: signature, argument: argument, parameter: parameter}
	}
	return nil
}

func (t *queryTab) functionHelp(engine database.Engine) *sqlSignatureHelp {
	sel := t.state.Selection()
	if sel.Anchor != sel.Head || t.signatureDismissed {
		return nil
	}
	version := t.state.Buffer.Version()
	if !t.signatureCached || version != t.signatureVersion || sel.Head != t.signatureOffset || engine != t.signatureEngine {
		t.signatureCached = true
		t.signatureVersion, t.signatureOffset, t.signatureEngine = version, sel.Head, engine
		t.signatureHelp = functionArgumentHelp(t.state.Buffer.String(), sel.Head, engine)
	}
	return t.signatureHelp
}

func functionHelpView(c *ui.Context, k palette, help *sqlSignatureHelp) {
	s := help.signature
	ui.Row(c.Key("sql-signature-help")).FillWidth().Height(27).Shrink(0).MinWidth(0).Padding(0, 10).Gap(5).AlignItems(ui.Center).Background(k.header).Clip().Label("SQL function arguments").Tooltip(s.label() + "\n" + s.description).Children(func() {
		ui.Text(c, s.name+"(").SingleLine().Font("JetBrains Mono").FontSize(11).TextColor(k.muted).Shrink(0)
		for i, param := range s.params {
			color := k.muted
			weight := 400
			if i == help.parameter {
				color = k.text
				weight = 600
			}
			ui.Text(c, param).SingleLine().Font("JetBrains Mono").FontSize(11).FontWeight(weight).TextColor(color).Shrink(0).Label(fmt.Sprintf("Function parameter %d", i+1))
			if i+1 < len(s.params) {
				sep := ","
				if s.separator != "" {
					sep = strings.TrimSpace(s.separator)
				}
				ui.Text(c, sep).FontSize(11).TextColor(k.muted).Shrink(0)
			}
		}
		ui.Text(c, ")").FontSize(11).TextColor(k.muted).Shrink(0)
		ui.Text(c, fmt.Sprintf("Argument %d · %s", help.argument+1, s.description)).SingleLine().MinWidth(0).FontSize(11).TextColor(k.muted)
	})
}
