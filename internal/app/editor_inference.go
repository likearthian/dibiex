package app

import (
	"strings"

	"dbcraft/internal/database"
)

const inferenceDepthLimit = 16
const inferenceScopeLimit = 128

type sqlInference struct {
	engine        database.Engine
	schemas       []database.Schema
	metadata      map[string]database.TableMetadata
	missing       []database.Table
	scopes        int
	work          int
	physicalCache map[string][]database.Column
}

type inferredSource struct {
	name    string
	path    []string
	query   *inferredScope
	names   []string
	cte     bool
	binding *inferredSource
}

type inferredScope struct {
	inference   *sqlInference
	tokens      []completionToken
	parent      *inferredScope
	ctes        map[string]*inferredSource
	sources     []*inferredSource
	children    map[int]*inferredScope
	body        int
	depth       int
	resolving   bool
	resolved    bool
	output      []database.Column
	outputKnown bool
}

func inferenceName(name string, engine database.Engine) string {
	if engine == database.PostgreSQL {
		return name
	}
	return strings.ToLower(name)
}

func (in *sqlInference) name(name string) string { return inferenceName(name, in.engine) }

func tokenClose(tokens []completionToken, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		if tokens[i].text == "(" {
			depth++
		}
		if tokens[i].text == ")" {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(tokens)
}

func queryTokens(tokens []completionToken) bool {
	return len(tokens) > 0 && (tokens[0].keyword("SELECT") || tokens[0].keyword("WITH"))
}

func tokenNames(tokens []completionToken) []string {
	var names []string
	for i, t := range tokens {
		if i%2 == 0 {
			if !t.identifier {
				return nil
			}
			names = append(names, t.text)
		} else if t.text != "," {
			return nil
		}
	}
	return names
}

func tokenPath(tokens []completionToken) []string {
	if len(tokens) == 0 || len(tokens)%2 == 0 {
		return nil
	}
	var path []string
	for i, t := range tokens {
		if i%2 == 0 {
			if !t.identifier || !t.quoted && completionReserved(t.text) {
				return nil
			}
			path = append(path, t.text)
		} else if t.text != "." {
			return nil
		}
	}
	return path
}

func (in *sqlInference) scope(tokens []completionToken, parent *inferredScope, ctes map[string]*inferredSource, depth int) *inferredScope {
	if depth > inferenceDepthLimit || in.scopes >= inferenceScopeLimit {
		return nil
	}
	in.scopes++
	s := &inferredScope{inference: in, tokens: tokens, parent: parent, depth: depth, ctes: map[string]*inferredSource{}, children: map[int]*inferredScope{}}
	for name, source := range ctes {
		s.ctes[name] = source
	}
	i := 0
	if len(tokens) > 0 && tokens[0].keyword("WITH") {
		i++
		recursive := i < len(tokens) && tokens[i].keyword("RECURSIVE")
		if recursive {
			i++
		}
		for i < len(tokens) && tokens[i].identifier {
			source := &inferredSource{name: tokens[i].text, cte: true}
			i++
			if i < len(tokens) && tokens[i].text == "(" {
				end := tokenClose(tokens, i)
				source.names = tokenNames(tokens[i+1 : end])
				i = end + 1
			}
			if i >= len(tokens) || !tokens[i].keyword("AS") {
				break
			}
			i++
			if i < len(tokens) && tokens[i].keyword("NOT") {
				i++
			}
			if i < len(tokens) && tokens[i].keyword("MATERIALIZED") {
				i++
			}
			if i >= len(tokens) || tokens[i].text != "(" {
				break
			}
			open, end := i, tokenClose(tokens, i)
			if recursive {
				s.ctes[in.name(source.name)] = source
			}
			source.query = in.scope(tokens[open+1:end], nil, s.ctes, depth+1)
			s.children[open] = source.query
			s.ctes[in.name(source.name)] = source
			i = end + 1
			if i >= len(tokens) || tokens[i].text != "," {
				break
			}
			i++
		}
	}
	s.body = min(i, len(tokens))
	// Every set-operation branch has a separate alias scope. The first branch
	// defines output names, while the caret chooses its own branch below.
	end := len(tokens)
	for j := s.body; j < len(tokens); j++ {
		if tokens[j].text == "(" {
			j = tokenClose(tokens, j)
			continue
		}
		if tokens[j].keyword("UNION") || tokens[j].keyword("INTERSECT") || tokens[j].keyword("EXCEPT") {
			// BigQuery SELECT * EXCEPT(...) is a projection modifier.
			if tokens[j].keyword("EXCEPT") && j+1 < len(tokens) && tokens[j+1].text == "(" {
				continue
			}
			end = j
			break
		}
	}
	from := false
	for j := s.body; j < end; j++ {
		t := tokens[j]
		trigger := t.keyword("FROM") || t.keyword("JOIN") || t.keyword("UPDATE") || t.keyword("INTO") || from && t.text == ","
		if t.keyword("WHERE") || t.keyword("GROUP") || t.keyword("ORDER") || t.keyword("HAVING") || t.keyword("SET") || t.keyword("LIMIT") || t.keyword("QUALIFY") {
			from = false
		}
		if !trigger {
			if t.text == "(" {
				j = tokenClose(tokens, j)
			}
			continue
		}
		from = true
		j++
		lateral := j < end && tokens[j].keyword("LATERAL")
		if lateral {
			j++
		}
		if j >= end {
			break
		}
		source := &inferredSource{}
		if tokens[j].text == "(" {
			open, close := j, tokenClose(tokens, j)
			if queryTokens(tokens[open+1 : close]) {
				var outer *inferredScope
				if lateral {
					outer = &inferredScope{inference: in, sources: append([]*inferredSource(nil), s.sources...), parent: s.parent, ctes: s.ctes}
				}
				source.query = in.scope(tokens[open+1:close], outer, s.ctes, depth+1)
				s.children[open] = source.query
			}
			j = close
		} else if tokens[j].identifier {
			source.path = []string{tokens[j].text}
			for j+2 < end && tokens[j+1].text == "." && tokens[j+2].identifier {
				j += 2
				source.path = append(source.path, tokens[j].text)
			}
			source.name = source.path[len(source.path)-1]
			if len(source.path) == 1 {
				if cte := s.ctes[in.name(source.name)]; cte != nil {
					source.binding, source.cte = cte, true
				}
			}
			// Table-valued functions have no inferred output until their schema is
			// known; never mistake them for a same-named physical table.
			if j+1 < end && tokens[j+1].text == "(" {
				j = tokenClose(tokens, j+1)
				source.path = nil
			}
		} else {
			j--
			continue
		}
		if j+1 < end && tokens[j+1].keyword("AS") {
			j++
		}
		if j+1 < end && tokens[j+1].identifier && (tokens[j+1].quoted || !completionReserved(tokens[j+1].text)) {
			j++
			source.name = tokens[j].text
		}
		if j+1 < end && tokens[j+1].text == "(" {
			close := tokenClose(tokens, j+1)
			if names := tokenNames(tokens[j+2 : close]); names != nil {
				source.names = names
				j = close
			}
		}
		if len(s.sources) < 32 {
			s.sources = append(s.sources, source)
		}
	}
	return s
}

func (s *inferredScope) active(caret int) *inferredScope {
	if s == nil {
		return nil
	}
	tokens := s.tokens
	// Choose the set-operation branch before looking for nested subqueries, so
	// a scalar subquery in a later branch inherits that branch's aliases.
	for i := s.body; i < len(tokens); i++ {
		if tokens[i].text == "(" {
			i = tokenClose(tokens, i)
			continue
		}
		if tokens[i].keyword("UNION") || tokens[i].keyword("INTERSECT") || tokens[i].keyword("EXCEPT") {
			if tokens[i].keyword("EXCEPT") && i+1 < len(tokens) && tokens[i+1].text == "(" {
				continue
			}
			if caret <= tokens[i].end {
				break
			}
			i++
			if i < len(tokens) && (tokens[i].keyword("ALL") || tokens[i].keyword("DISTINCT")) {
				i++
			}
			child := s.inference.scope(tokens[i:], s.parent, s.ctes, s.depth+1)
			if child != nil {
				return child.active(caret)
			}
			break
		}
	}
	for i := 0; i < len(tokens); i++ {
		if tokens[i].text != "(" {
			continue
		}
		end := tokenClose(tokens, i)
		inside := caret >= tokens[i].end && (end == len(tokens) || caret <= tokens[end].start)
		if !inside {
			i = end
			continue
		}
		if child, ok := s.children[i]; ok {
			if child != nil {
				return child.active(caret)
			}
			return s
		}
		if queryTokens(tokens[i+1 : end]) {
			child := s.inference.scope(tokens[i+1:end], s, s.ctes, s.depth+1)
			if child != nil {
				return child.active(caret)
			}
			return s
		}
		// Parenthesized expressions may themselves contain scalar subqueries.
	}
	return s
}

func (in *sqlInference) physical(path []string) []database.Column {
	key := in.name(strings.Join(path, "\x00"))
	if in.physicalCache == nil {
		in.physicalCache = map[string][]database.Column{}
	}
	if cols, ok := in.physicalCache[key]; ok {
		return cols
	}
	in.physicalCache[key] = nil
	var matches []database.Table
	for _, schema := range in.schemas {
		for _, table := range schema.Tables {
			in.work++
			if in.work > 100000 {
				return nil
			}
			parts := []string{table.Project, table.Schema, table.Name}
			matchPath := path
			if in.engine == database.BigQuery && len(path) == 1 {
				matchPath = strings.Split(path[0], ".")
			}
			match := len(matchPath) > 0 && len(matchPath) <= len(parts)
			if match {
				for i, name := range matchPath {
					if in.name(name) != in.name(parts[len(parts)-len(matchPath)+i]) {
						match = false
						break
					}
				}
			}
			if match {
				matches = append(matches, table)
				if len(matches) > 1 {
					return nil
				}
			}
		}
	}
	if len(matches) != 1 {
		return nil
	}
	table := matches[0]
	if m, ok := in.metadata[table.ID()]; ok {
		in.physicalCache[key] = m.Columns
		return m.Columns
	}
	for _, missing := range in.missing {
		if missing.ID() == table.ID() {
			return nil
		}
	}
	if len(in.missing) < 4 {
		in.missing = append(in.missing, table)
	}
	return nil
}

func (source *inferredSource) columns(in *sqlInference) []database.Column {
	var cols []database.Column
	if source.binding != nil {
		cols = source.binding.columns(in)
	} else if source.query != nil {
		cols = source.query.outputs()
	} else if !source.cte && len(source.path) > 0 {
		cols = in.physical(source.path)
	}
	if len(source.names) == 0 {
		return cols
	}
	known := source.query == nil || source.query.outputKnown
	var renamed []database.Column
	for i, name := range source.names {
		if len(renamed) == completionLimit {
			break
		}
		col := database.Column{Name: name}
		if known && i < len(cols) {
			col = cols[i]
			col.Name = name
		}
		renamed = append(renamed, col)
	}
	if known && len(cols) > len(source.names) {
		renamed = append(renamed, cols[len(source.names):]...)
		if len(renamed) > completionLimit {
			renamed = renamed[:completionLimit]
		}
	}
	return renamed
}

func (s *inferredScope) localColumns() []database.Column {
	var cols []database.Column
	for _, source := range s.sources {
		cols = append(cols, source.columns(s.inference)...)
		if len(cols) >= completionLimit {
			return cols[:completionLimit]
		}
	}
	return cols
}

func nestedColumns(cols []database.Column, path []string, engine database.Engine) []database.Column {
	for _, name := range path {
		var next []database.Column
		for _, col := range cols {
			if inferenceName(col.Name, engine) == inferenceName(name, engine) {
				next = col.Fields
				break
			}
		}
		cols = next
	}
	return cols
}

func (s *inferredScope) qualified(path []string) ([]database.Column, bool) {
	if len(path) == 0 {
		return nil, false
	}
	for scope := s; scope != nil; scope = scope.parent {
		var matches []*inferredSource
		for _, source := range scope.sources {
			if s.inference.name(source.name) == s.inference.name(path[0]) {
				matches = append(matches, source)
			}
		}
		if len(matches) > 0 {
			if len(matches) != 1 {
				return nil, true
			}
			return nestedColumns(matches[0].columns(s.inference), path[1:], s.inference.engine), true
		}
		// An explicit alias hides the original relation name.
		for _, source := range scope.sources {
			if len(source.path) > 0 && s.inference.name(path[0]) == s.inference.name(source.path[len(source.path)-1]) {
				return nil, true
			}
		}
		if source := scope.ctes[s.inference.name(path[0])]; source != nil {
			return nestedColumns(source.columns(s.inference), path[1:], s.inference.engine), true
		}
	}
	return nil, false
}

func (s *inferredScope) visibleColumns() []database.Column {
	var cols []database.Column
	seen := map[string]bool{}
	seenColumns := map[string]bool{}
	for scope := s; scope != nil; scope = scope.parent {
		var local []database.Column
		for _, source := range scope.sources {
			key := s.inference.name(source.name)
			if seen[key] {
				continue
			}
			seen[key] = true
			local = append(local, source.columns(s.inference)...)
			if len(local) >= completionLimit {
				local = local[:completionLimit]
				break
			}
		}
		for _, col := range local {
			if !seenColumns[s.inference.name(col.Name)] {
				cols = append(cols, col)
				if len(cols) == completionLimit {
					return cols
				}
			}
		}
		for _, col := range local {
			seenColumns[s.inference.name(col.Name)] = true
		}
	}
	return cols
}

func (s *inferredScope) tableContext(caret int) bool {
	tables := false
	for i := s.body; i < len(s.tokens) && s.tokens[i].start < caret; i++ {
		t := s.tokens[i]
		if t.text == "(" {
			i = tokenClose(s.tokens, i)
			continue
		}
		if t.keyword("FROM") || t.keyword("JOIN") || t.keyword("UPDATE") || t.keyword("INTO") {
			tables = true
		}
		if t.keyword("SELECT") || t.keyword("WHERE") || t.keyword("ON") || t.keyword("SET") || t.keyword("HAVING") || t.keyword("ORDER") || t.keyword("GROUP") || t.keyword("VALUES") || t.keyword("RETURNING") {
			tables = false
		}
	}
	return tables
}

func (s *inferredScope) outputAliasesVisible(caret int) bool {
	visible := false
	for i := s.body; i < len(s.tokens) && s.tokens[i].start < caret; i++ {
		t := s.tokens[i]
		if t.text == "(" {
			i = tokenClose(s.tokens, i)
			continue
		}
		if t.keyword("SELECT") || t.keyword("FROM") || t.keyword("WHERE") || t.keyword("HAVING") {
			visible = false
		}
		if t.keyword("ORDER") || t.keyword("GROUP") || t.keyword("QUALIFY") && s.inference.engine == database.BigQuery {
			visible = true
		}
	}
	return visible
}

func unambiguousColumns(cols []database.Column, engine database.Engine) []database.Column {
	counts := map[string]int{}
	for _, col := range cols {
		counts[inferenceName(col.Name, engine)]++
	}
	var unique []database.Column
	for _, col := range cols {
		if counts[inferenceName(col.Name, engine)] == 1 {
			unique = append(unique, col)
		}
	}
	return unique
}

func (s *inferredScope) reference(path []string) (database.Column, bool) {
	if len(path) == 0 {
		return database.Column{}, false
	}
	var cols []database.Column
	if len(path) > 1 {
		cols, _ = s.qualified(path[:len(path)-1])
	} else {
		cols = s.visibleColumns()
	}
	var result database.Column
	count := 0
	for _, col := range cols {
		if s.inference.name(col.Name) == s.inference.name(path[len(path)-1]) {
			result = col
			count++
		}
	}
	return result, count == 1
}

func (s *inferredScope) projection(tokens []completionToken) []database.Column {
	if len(tokens) == 0 {
		return []database.Column{{}}
	}
	// Only aliases at the expression's own level name its output. AS inside
	// CAST or a scalar subquery does not name the containing expression.
	alias := ""
	for i := 0; i < len(tokens); i++ {
		if tokens[i].text == "(" {
			i = tokenClose(tokens, i)
			continue
		}
		if tokens[i].keyword("AS") && i+2 == len(tokens) && tokens[i+1].identifier {
			alias = tokens[i+1].text
			tokens = tokens[:i]
			break
		}
	}
	if alias == "" && len(tokens) > 1 {
		last, prev := tokens[len(tokens)-1], tokens[len(tokens)-2]
		if last.identifier && (last.quoted || !completionReserved(last.text)) && (prev.identifier || prev.text == ")" || prev.text == "" || prev.text >= "0" && prev.text <= "9") && !prev.keyword("COLLATE") {
			alias = last.text
			tokens = tokens[:len(tokens)-1]
		}
	}
	if alias != "" {
		col, ok := s.reference(tokenPath(tokens))
		if !ok {
			col = database.Column{}
		}
		col.Name = alias
		return []database.Column{col}
	}
	if len(tokens) == 1 && tokens[0].text == "*" {
		return s.localColumns()
	}
	if len(tokens) > 2 && tokens[len(tokens)-1].text == "*" && tokens[len(tokens)-2].text == "." {
		cols, _ := s.qualified(tokenPath(tokens[:len(tokens)-2]))
		return cols
	}
	if path := tokenPath(tokens); len(path) > 0 {
		col, ok := s.reference(path)
		if !ok {
			col = database.Column{Name: path[len(path)-1]}
		}
		return []database.Column{col}
	}
	return []database.Column{{}}
}

func (s *inferredScope) outputs() []database.Column {
	if s == nil || s.resolving {
		return nil
	}
	if s.resolved {
		return s.output
	}
	s.resolving = true
	defer func() { s.resolving = false; s.resolved = true }()
	tokens := s.tokens
	start := s.body
	if start >= len(tokens) || !tokens[start].keyword("SELECT") {
		return nil
	}
	start++
	if start < len(tokens) && tokens[start].keyword("AS") {
		return nil
	}
	if start < len(tokens) && (tokens[start].keyword("DISTINCT") || tokens[start].keyword("ALL")) {
		start++
		if start+1 < len(tokens) && tokens[start].keyword("ON") && tokens[start+1].text == "(" {
			start = tokenClose(tokens, start+1) + 1
		}
	}
	start = min(start, len(tokens))
	s.outputKnown = true
	for i := start; i <= len(tokens); i++ {
		if i < len(tokens) && tokens[i].text == "(" {
			i = tokenClose(tokens, i)
			if i < len(tokens) {
				continue
			}
		}
		end := i >= len(tokens) || tokens[i].keyword("FROM") || tokens[i].keyword("WHERE") || tokens[i].keyword("UNION") || tokens[i].keyword("INTERSECT") || tokens[i].keyword("ORDER") || tokens[i].keyword("LIMIT") || tokens[i].keyword("GROUP") || tokens[i].keyword("HAVING") || tokens[i].keyword("QUALIFY") || tokens[i].keyword("EXCEPT") && (i+1 >= len(tokens) || tokens[i+1].text != "(") || tokens[i].text == ";"
		if end || tokens[i].text == "," {
			cols := s.projection(tokens[start:i])
			if cols == nil {
				s.outputKnown = false
			}
			s.output = append(s.output, cols...)
			if len(s.output) >= completionLimit {
				s.output = s.output[:completionLimit]
				break
			}
			start = i + 1
		}
		if end {
			break
		}
	}
	return s.output
}

func inferCompletionScope(c completionContext, engine database.Engine, schemas []database.Schema, metadata map[string]database.TableMetadata) (*inferredScope, *sqlInference) {
	in := &sqlInference{engine: engine, schemas: schemas, metadata: metadata}
	s := in.scope(c.statement, nil, nil, 0)
	return s.active(c.caret), in
}
