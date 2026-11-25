package app

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui/editor"
)

const completionLimit = 512
const completionSQLLimit = 256 * 1024

const sqlCompletionKeywords = "SELECT FROM WHERE JOIN LEFT RIGHT INNER OUTER FULL CROSS ON AS AND OR NOT NULL IS IN EXISTS BETWEEN LIKE DISTINCT ALL GROUP BY HAVING ORDER ASC DESC LIMIT OFFSET UNION INTERSECT EXCEPT WITH RECURSIVE INSERT INTO VALUES UPDATE SET DELETE RETURNING CREATE TABLE VIEW INDEX ALTER DROP CASE WHEN THEN ELSE END CAST OVER PARTITION ROWS RANGE CURRENT ROW PRECEDING FOLLOWING TRUE FALSE"

func completionWords(engine database.Engine) (string, string) {
	keywords := sqlCompletionKeywords
	functions := "COUNT SUM AVG MIN MAX COALESCE NULLIF ABS ROUND LOWER UPPER LENGTH SUBSTR TRIM REPLACE CURRENT_DATE CURRENT_TIMESTAMP ROW_NUMBER RANK DENSE_RANK LAG LEAD"
	switch engine {
	case database.PostgreSQL:
		keywords += " ILIKE LATERAL MATERIALIZED CONFLICT DO NOTHING FILTER NULLS FIRST LAST FETCH"
		functions += " NOW STRING_AGG ARRAY_AGG JSONB_AGG JSONB_BUILD_OBJECT GENERATE_SERIES DATE_TRUNC EXTRACT UNNEST"
	case database.SQLite:
		keywords += " PRAGMA WITHOUT ROWID AUTOINCREMENT GLOB STRICT ATTACH DETACH REINDEX"
		functions += " IFNULL TOTAL GROUP_CONCAT STRFTIME DATETIME JULIANDAY JSON_EXTRACT"
	case database.MySQL:
		keywords += " AUTO_INCREMENT ENGINE CHARSET COLLATE UNSIGNED ZEROFILL ENUM DESCRIBE SHOW CHANGE MODIFY RENAME TRUNCATE DUPLICATE IGNORE STRAIGHT_JOIN USE"
		functions += " IFNULL IF CONCAT CONCAT_WS GROUP_CONCAT DATE_FORMAT STR_TO_DATE NOW CURDATE JSON_EXTRACT JSON_UNQUOTE REGEXP_LIKE"
	case database.BigQuery:
		keywords += " QUALIFY STRUCT ARRAY OPTIONS CLUSTER PIVOT UNPIVOT SYSTEM_TIME"
		functions += " SAFE_CAST IF IFNULL COUNTIF ARRAY_AGG STRING_AGG UNNEST FORMAT_DATE DATE_TRUNC TIMESTAMP_TRUNC REGEXP_CONTAINS TO_JSON_STRING"
	}
	return keywords, functions
}

type completionToken struct {
	text       string
	start, end int
	identifier bool
	quoted     bool
}

// Lex only a bounded statement. Strings/comments are omitted; blocked reports
// an unfinished literal, comment or quoted identifier at the caret.
func completionTokens(sql string, engine database.Engine) ([]completionToken, bool) {
	var tokens []completionToken
	for i := 0; i < len(sql); {
		start := i
		r, size := utf8.DecodeRuneInString(sql[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		if sqlLineComment(sql, i, engine) {
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				return tokens, true
			}
			i += end + 1
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
			if depth > 0 {
				return tokens, true
			}
			continue
		}
		if r == '$' && engine == database.PostgreSQL {
			j := i + 1
			for j < len(sql) && (sql[j] == '_' || sql[j] >= 'a' && sql[j] <= 'z' || sql[j] >= 'A' && sql[j] <= 'Z' || j > i+1 && sql[j] >= '0' && sql[j] <= '9') {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					return tokens, true
				}
				i = j + 1 + end + len(tag)
				continue
			}
		}
		if r == '\'' || r == '"' || r == '`' || r == '[' && engine == database.SQLite {
			quote := byte(r)
			close := string(quote)
			if quote == '[' {
				close = "]"
			}
			literal := quote == '\'' || quote == '"' && (engine == database.BigQuery || engine == database.MySQL)
			if engine == database.BigQuery && strings.HasPrefix(sql[i:], strings.Repeat(string(quote), 3)) {
				close = strings.Repeat(string(quote), 3)
			}
			escaped := engine == database.BigQuery || (engine == database.MySQL && literal) || engine == database.PostgreSQL && literal && i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E')
			i += len(close)
			var value strings.Builder
			closed := false
			for i < len(sql) {
				if escaped && sql[i] == '\\' && i+1 < len(sql) {
					value.WriteByte(sql[i+1])
					i += 2
					continue
				}
				if strings.HasPrefix(sql[i:], close) {
					i += len(close)
					if len(close) == 1 && quote != '[' && i < len(sql) && sql[i] == quote {
						value.WriteByte(quote)
						i++
						continue
					}
					closed = true
					break
				}
				value.WriteByte(sql[i])
				i++
			}
			if !closed {
				return tokens, true
			}
			if !literal {
				tokens = append(tokens, completionToken{value.String(), start, i, true, true})
			}
			continue
		}
		if unicode.IsLetter(r) || r == '_' {
			i += size
			for i < len(sql) {
				r, size = utf8.DecodeRuneInString(sql[i:])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' {
					break
				}
				i += size
			}
			word := sql[start:i]
			if engine == database.PostgreSQL {
				word = strings.ToLower(word)
			}
			tokens = append(tokens, completionToken{word, start, i, true, false})
			continue
		}
		i += size
		tokens = append(tokens, completionToken{sql[start:i], start, i, false, false})
	}
	return tokens, false
}

func (t completionToken) keyword(word string) bool {
	return !t.quoted && strings.EqualFold(t.text, word)
}

type completionContext struct {
	start             int
	caret             int
	prefix            string
	qualifier         []string
	tables            bool
	openBacktick      bool
	before, statement []completionToken
	blocked           bool
}

var bigQueryProjectPrefix = regexp.MustCompile("(?:`(?:[a-zA-Z][a-zA-Z0-9-]*)?|[a-zA-Z][a-zA-Z0-9-]*)$")

func sqlCompletionContext(sql string, offset int, engine database.Engine) completionContext {
	c := completionContext{start: offset}
	if offset < 0 || offset > len(sql) || len(sql) > completionSQLLimit {
		c.blocked = true
		return c
	}
	lo, hi := statementRange(sql, offset, engine)
	c.caret = offset - lo
	c.before, c.blocked = completionTokens(sql[lo:offset], engine)
	if engine == database.BigQuery {
		if loc := bigQueryProjectPrefix.FindStringIndex(sql[lo:offset]); loc != nil {
			start := lo + loc[0]
			if start > lo && unicode.IsSpace(rune(sql[start-1])) && strings.ContainsAny(sql[start:offset], "`-") {
				before, blocked := completionTokens(sql[lo:start], engine)
				if !blocked && len(before) > 0 {
					last := before[len(before)-1]
					if last.keyword("FROM") || last.keyword("JOIN") || last.keyword("UPDATE") || last.keyword("INTO") {
						c.before, c.blocked = before, false
						c.start, c.prefix, c.tables = start, strings.TrimPrefix(sql[start:offset], "`"), true
						c.openBacktick = sql[start] == '`'
						c.statement, _ = completionTokens(sql[lo:hi], engine)
						c.blocked = len(c.statement) > 32768
						return c
					}
				}
			}
		}
	}
	if c.blocked {
		return c
	}
	c.statement, _ = completionTokens(sql[lo:hi], engine)
	if len(c.statement) > 32768 {
		c.blocked = true
		return c
	}
	if n := len(c.before); n > 0 {
		last := c.before[n-1]
		if last.identifier && !last.quoted && last.end == offset-lo {
			c.start, c.prefix = lo+last.start, last.text
			c.before = c.before[:n-1]
		}
	}
	n := len(c.before)
	if n > 0 && c.before[n-1].text == "." {
		for i := n - 2; i >= 0 && c.before[i].identifier; i -= 2 {
			c.qualifier = append([]string{c.before[i].text}, c.qualifier...)
			if i == 0 || c.before[i-1].text != "." {
				break
			}
		}
	}
	// The current clause determines whether a comma starts another relation.
	for i := len(c.before) - 1; i >= 0; i-- {
		t := c.before[i]
		if t.keyword("FROM") || t.keyword("JOIN") || t.keyword("UPDATE") || t.keyword("INTO") {
			c.tables = true
			break
		}
		if t.keyword("SELECT") || t.keyword("WHERE") || t.keyword("ON") || t.keyword("SET") || t.keyword("HAVING") || t.keyword("ORDER") || t.keyword("GROUP") {
			break
		}
	}
	return c
}

type completionRelation struct {
	path  []string
	alias string
}

func completionRelations(tokens []completionToken) []completionRelation {
	var relations []completionRelation
	inFrom := false
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		trigger := t.keyword("FROM") || t.keyword("JOIN") || t.keyword("UPDATE") || t.keyword("INTO") || inFrom && t.text == ","
		if t.keyword("WHERE") || t.keyword("ON") || t.keyword("GROUP") || t.keyword("ORDER") || t.keyword("SET") {
			inFrom = false
		}
		if !trigger || i+1 >= len(tokens) || !tokens[i+1].identifier {
			continue
		}
		inFrom = true
		i++
		r := completionRelation{path: []string{tokens[i].text}}
		for i+2 < len(tokens) && tokens[i+1].text == "." && tokens[i+2].identifier {
			i += 2
			r.path = append(r.path, tokens[i].text)
		}
		if i+1 < len(tokens) && tokens[i+1].keyword("AS") {
			i++
		}
		if i+1 < len(tokens) && tokens[i+1].identifier && (tokens[i+1].quoted || !completionReserved(tokens[i+1].text)) {
			i++
			r.alias = tokens[i].text
		}
		relations = append(relations, r)
		if len(relations) == 32 {
			break
		}
	}
	return relations
}

func completionReserved(name string) bool {
	return strings.Contains(" "+sqlCompletionKeywords+" QUALIFY USING NATURAL WINDOW NULLS FETCH FOR ", " "+strings.ToUpper(name)+" ")
}

func completionIdentifier(name string, engine database.Engine) string {
	if engine == database.PostgreSQL && barePostgreSQLCompletion(name) {
		return name
	}
	if engine == database.BigQuery {
		return "`" + strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(name) + "`"
	}
	return database.QuoteIdentifierFor(engine, name)
}

// Returns only targeted missing metadata, never a whole-catalog column scan.
func sqlCompletions(c completionContext, engine database.Engine, schemas []database.Schema, metadata map[string]database.TableMetadata, projects ...string) ([]editor.Completion, []database.Table) {
	if c.blocked {
		return nil, nil
	}
	var items []editor.Completion
	var missing []database.Table
	seen := map[string]bool{}
	add := func(label, insert, detail string, kind editor.CompletionKind) {
		if label == "" || len(items) >= completionLimit || !strings.HasPrefix(strings.ToLower(label), strings.ToLower(c.prefix)) {
			return
		}
		key := insert + "\x00" + detail
		if seen[key] {
			return
		}
		seen[key] = true
		item := editor.Completion{Label: label, Insert: insert, Detail: detail, Kind: kind}
		if c.openBacktick {
			if !strings.HasPrefix(insert, "`") {
				return
			}
			item.FilterText = "`" + label
		}
		items = append(items, item)
	}
	if engine == database.BigQuery && c.tables && len(c.qualifier) == 0 {
		for _, project := range projects {
			add(project, completionIdentifier(project, engine), "BigQuery project", editor.CompletionModule)
		}
		for _, schema := range schemas {
			add(schema.Project, completionIdentifier(schema.Project, engine), "BigQuery project", editor.CompletionModule)
		}
	}
	scope, inference := inferCompletionScope(c, engine, schemas, metadata)
	if scope != nil {
		c.tables = scope.tableContext(c.caret)
	}
	if scope != nil {
		if len(c.qualifier) == 0 {
			var cteNames []string
			for name := range scope.ctes {
				cteNames = append(cteNames, name)
			}
			sort.Strings(cteNames)
			for _, name := range cteNames {
				source := scope.ctes[name]
				add(source.name, completionIdentifier(source.name, engine), "CTE", editor.CompletionType)
			}
			if !c.tables {
				for current := scope; current != nil; current = current.parent {
					for _, source := range current.sources {
						if source.name != "" {
							add(source.name, completionIdentifier(source.name, engine), "relation alias", editor.CompletionVariable)
						}
					}
				}
				for _, col := range unambiguousColumns(scope.visibleColumns(), engine) {
					add(col.Name, completionIdentifier(col.Name, engine), col.Type, editor.CompletionField)
				}
				if scope.outputAliasesVisible(c.caret) {
					for _, col := range unambiguousColumns(scope.outputs(), engine) {
						add(col.Name, completionIdentifier(col.Name, engine), "query output", editor.CompletionField)
					}
				}
			}
		} else if !c.tables {
			cols, found := scope.qualified(c.qualifier)
			if !found {
				cols = inference.physical(c.qualifier)
			}
			for _, col := range unambiguousColumns(cols, engine) {
				add(col.Name, completionIdentifier(col.Name, engine), col.Type, editor.CompletionField)
			}
		}
		missing = inference.missing
	}

	for _, schema := range schemas {
		qual := strings.Join(c.qualifier, ".")
		schemaMatch := strings.EqualFold(qual, schema.Name) || strings.EqualFold(qual, schema.Project+"."+schema.Name)
		if len(c.qualifier) == 0 || engine == database.BigQuery && strings.EqualFold(qual, schema.Project) {
			add(schema.Name, completionIdentifier(schema.Name, engine), "schema / dataset", editor.CompletionModule)
		}
		for _, table := range schema.Tables {
			if len(c.qualifier) == 0 || schemaMatch {
				insert := completionTableReference(table, engine)
				if schemaMatch {
					insert = completionIdentifier(table.Name, engine)
				}
				add(table.Name, insert, table.Label()+" · "+table.Kind, editor.CompletionType)
			}
		}
	}
	if len(c.qualifier) == 0 {
		keywords, functions := completionWords(engine)
		for _, word := range strings.Fields(keywords) {
			add(word, word, "keyword", editor.CompletionKeyword)
		}
		if !c.tables {
			for _, word := range strings.Fields(functions) {
				detail := "function"
				if signature, ok := sqlFunctionCatalog[engine][word]; ok {
					detail = signature.label()
				}
				add(word, word, detail, editor.CompletionFunction)
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !c.tables && (items[i].Kind == editor.CompletionField) != (items[j].Kind == editor.CompletionField) {
			return items[i].Kind == editor.CompletionField
		}
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
	return items, missing
}

func (t *queryTab) cancelCompletion() {
	t.completionID++
	if t.completionCancel != nil {
		t.completionCancel()
		t.completionCancel = nil
	}
}

func (a *App) completionCurrent(t *queryTab, r editor.CompletionRequest, id uint64) bool {
	if a.closed || t.completionID != id || a.currentEditor() != t || t.state.Buffer.Version() != r.Version {
		return false
	}
	if a.connectionWorkspace != nil && a.connectionWorkspace.active != a {
		return false
	}
	sel := t.state.Selection()
	return sel.Head == r.Offset && sel.Anchor == r.Offset
}

func completionMissingSchema(c completionContext, schemas []database.Schema) *database.Schema {
	relations := completionRelations(c.statement)
	for _, s := range schemas {
		if s.Loaded {
			continue
		}
		qual := strings.Join(c.qualifier, ".")
		match := qual != "" && (strings.EqualFold(qual, s.Name) || strings.EqualFold(qual, s.Project+"."+s.Name))
		for _, r := range relations {
			path := r.path
			if len(path) == 1 {
				path = strings.Split(path[0], ".")
			}
			if len(path) < 2 {
				continue
			}
			if strings.EqualFold(path[len(path)-2], s.Name) && (len(path) < 3 || strings.EqualFold(path[len(path)-3], s.Project)) {
				match = true
			}
		}
		if match {
			copy := s
			return &copy
		}
	}
	return nil
}

func (a *App) completeSQL(t *queryTab, r editor.CompletionRequest) {
	t.cancelCompletion()
	id := t.completionID
	engine := t.engine
	if a.client != nil {
		engine = a.client.Engine()
	}
	input := t.state.Buffer.String()
	c := sqlCompletionContext(input, r.Offset, engine)
	items, missing := sqlCompletions(c, engine, a.schemas, a.metadata, a.bigQueryProjects()...)
	t.state.ShowCompletions(c.start, items)
	if c.blocked || a.client == nil || a.win == nil {
		return
	}
	// Only qualified, known datasets trigger lazy object discovery.
	schema := completionMissingSchema(c, a.schemas)
	if len(missing) == 0 && schema == nil {
		return
	}
	client, win, version := a.client, a.win, a.metadataVersion
	runner, pipeline := a.catalogRunner()
	ctx, cancel := context.WithTimeout(a.connectionContext(), 15*time.Second)
	t.completionCancel = cancel
	go func() {
		defer cancel()
		// Debounce targeted reads while the user is still typing.
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		loaded := map[string]database.TableMetadata{}
		var tables []database.Table
		var schemaErr error
		if schema != nil {
			if pipeline != nil {
				tables, schemaErr = pipeline.Schema(ctx, *schema, false)
			} else {
				tables, schemaErr = runner.SchemaTables(ctx, *schema)
			}
			if schemaErr == nil {
				discovered := *schema
				discovered.Tables, discovered.Loaded = tables, true
				_, extra := sqlCompletions(c, engine, []database.Schema{discovered}, nil)
				for _, table := range extra {
					if len(missing) < 4 {
						missing = append(missing, table)
					}
				}
			}
		}
		for _, table := range missing {
			if ctx.Err() != nil {
				return
			}
			var m database.TableMetadata
			var err error
			if pipeline != nil {
				m, err = pipeline.Table(ctx, table, false)
			} else {
				m, err = runner.TableMetadata(ctx, table)
			}
			if err == nil {
				loaded[table.ID()] = m
			}
		}
		if ctx.Err() != nil {
			return
		}
		win.Update(func() {
			if a.closed || a.client != client || a.metadataVersion != version || t.completionID != id {
				return
			}
			t.completionCancel = nil
			if a.metadata == nil {
				a.metadata = map[string]database.TableMetadata{}
			}
			for key, m := range loaded {
				if old, ok := a.metadata[key]; !ok || old.FetchedAt.Before(m.FetchedAt) {
					a.metadata[key] = m
				}
			}
			if schema != nil && schemaErr == nil {
				for i := range a.schemas {
					if a.schemas[i].ID() == schema.ID() && !a.schemas[i].Loaded {
						a.schemas[i].Tables, a.schemas[i].Loaded = tables, true
						a.schemas[i].FetchedAt = time.Now().UTC()
					}
				}
			}
			a.refreshSQLCompletions(t, input, r, id, engine)
		})
	}()
}

// MyGo filters an open list without asking the provider again as its prefix
// changes. Merge late metadata into that list when only the prefix was edited;
// changes to the surrounding SQL must invalidate the original request.
func (a *App) refreshSQLCompletions(t *queryTab, input string, r editor.CompletionRequest, id uint64, engine database.Engine) {
	if !t.state.Focused() {
		return
	}
	sel := t.state.Selection()
	current := editor.CompletionRequest{Offset: sel.Head, Version: t.state.Buffer.Version()}
	if !a.completionCurrent(t, current, id) {
		return
	}
	sql := t.state.Buffer.String()
	old := sqlCompletionContext(input, r.Offset, engine)
	next := sqlCompletionContext(sql, current.Offset, engine)
	if old.blocked || next.blocked || old.start != next.start || input[:old.start] != sql[:next.start] || input[r.Offset:] != sql[current.Offset:] {
		return
	}
	items, _ := sqlCompletions(next, engine, a.schemas, a.metadata, a.bigQueryProjects()...)
	t.state.ShowCompletions(next.start, items)
}
