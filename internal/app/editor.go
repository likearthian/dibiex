package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/jackc/pgx/v5/pgconn"
)

var bigQueryPosition = regexp.MustCompile(`\[(\d+):(\d+)\]`)
var sqlWord = regexp.MustCompile(`[A-Za-z_][A-Za-z_0-9]*`)

func queryErrorPosition(err error, sql string) (line, column int) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Position > 0 {
		line, column = 1, 1
		for i, r := range []rune(sql) {
			if i >= int(pg.Position)-1 {
				break
			}
			if r == '\n' {
				line++
				column = 1
			} else {
				column++
			}
		}
		return
	}
	if pos := bigQueryPosition.FindStringSubmatch(err.Error()); len(pos) > 0 {
		line, _ = strconv.Atoi(pos[1])
		column, _ = strconv.Atoi(pos[2])
	}
	return
}

type queryTab struct {
	completionCancel   context.CancelFunc
	completionID       uint64
	signatureCached    bool
	signatureVersion   uint64
	signatureOffset    int
	signatureEngine    database.Engine
	signatureHelp      *sqlSignatureHelp
	signatureDismissed bool
	cursorFetch        bool
	session            *database.Client
	querySchemas       []database.Schema
	execution          queryExecution
	savedID            string
	id                 int
	name               string
	state              editor.State
	engine             database.Engine
	paramValues        map[string]queryParamValue
	tableView          *database.Table
}

func (a *App) currentEditor() *queryTab {
	if len(a.tabs) == 0 {
		return nil
	}
	return a.tabs[a.activeTab]
}

func (a *App) addQuery(sql string) {
	previous := a.currentEditor()
	a.storeQueryView(previous)
	a.nextTab++
	name := "console.sql"
	if a.nextTab > 1 {
		name = fmt.Sprintf("query-%d.sql", a.nextTab)
	}
	a.tabs = append(a.tabs, &queryTab{id: a.nextTab, name: name, state: editor.State{Buffer: editor.NewBuffer(sql), Font: ui.Font{Family: "JetBrains Mono", Size: 13}, Label: "SQL query editor"}})
	a.activeTab = len(a.tabs) - 1
	tab := a.tabs[a.activeTab]
	tab.execution = queryExecution{status: "Ready", selected: -1, line: 1}
	if previous == nil {
		a.storeQueryView(tab)
	}
	a.selectQueryCatalog(tab)
	a.loadQueryView(tab)
}

func (a *App) setQuery(sql string) {
	a.query = sql
	if len(a.tabs) > 0 {
		a.tabs[a.activeTab].state.Buffer.Set(sql)
	}
}

func (a *App) querySQL() string {
	if len(a.tabs) == 0 {
		a.executionLine = 1
		return ""
	}
	t := a.currentEditor()
	sql := t.state.Buffer.String()
	sel := t.state.Selection()
	start, end := min(sel.Anchor, sel.Head), max(sel.Anchor, sel.Head)
	if start == end {
		start, end = statementRange(sql, start, a.client.Engine())
	}
	a.executionLine = 1 + strings.Count(sql[:start], "\n")
	return sql[start:end]
}

func (a *App) sqlEditor(c *ui.Context) {
	t := a.currentEditor()
	if t == nil {
		return
	}
	k := paletteOf(c)
	t.state.Theme = editor.DefaultTheme(c.Theme())
	t.state.Theme.Background = k.editor
	t.state.Theme.ActiveLine = k.header.Alpha(.65)
	t.state.Theme.Selection = k.selected
	t.state.Theme.Cursor = k.text
	t.state.WrapGuides = []int{120}
	if c.Theme().Dark {
		t.state.Theme.Tokens[editor.TokenKeyword].Color = ui.Hex("#cf8e6d")
		t.state.Theme.Tokens[editor.TokenString].Color = ui.Hex("#6aab73")
		t.state.Theme.Tokens[editor.TokenNumber].Color = ui.Hex("#2aacb8")
		t.state.Theme.Tokens[editor.TokenComment].Color = ui.Hex("#7a7e85")
	}
	engine := a.client.Engine()
	if t.engine != engine {
		lang := *editor.LanguageFor("query.sql")
		lang.Name = string(engine) + " SQL"
		base := lang.Tokenizer
		keywords := map[database.Engine]string{
			database.SQLite:     "pragma without rowid autoincrement glob strict attach detach reindex",
			database.PostgreSQL: "ilike lateral returning conflict materialized over partition filter tablesample unnest jsonb timestamptz bytea",
			database.MySQL:      "auto_increment engine charset collate unsigned zerofill enum describe show databases tables change modify rename truncate duplicate ignore straight_join use",
			database.BigQuery:   "qualify unnest struct array safe_cast int64 float64 string bytes partition cluster options except replace pivot unpivot system_time",
		}
		words := " " + keywords[engine] + " "
		lang.Tokenizer = editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
			baseTokens, next := base.Tokenize(line, state, nil)
			out := tokens
			start := 0
			for _, tok := range baseTokens {
				if tok.Kind == editor.TokenText {
					pending := start
					for _, span := range sqlWord.FindAllStringIndex(line[start:tok.End], -1) {
						lo, hi := start+span[0], start+span[1]
						if strings.Contains(words, " "+strings.ToLower(line[lo:hi])+" ") {
							if lo > pending {
								out = append(out, editor.Token{End: lo, Kind: tok.Kind})
							}
							out = append(out, editor.Token{End: hi, Kind: editor.TokenKeyword})
							pending = hi
						}
					}
					if pending < tok.End {
						out = append(out, tok)
					}
				} else {
					out = append(out, tok)
				}
				start = tok.End
			}
			return out, next
		})
		t.state.Buffer.SetLanguage(&lang)
		t.engine = engine
	}
	t.state.OnCompletion = func(r editor.CompletionRequest) { a.completeSQL(t, r) }
	t.state.OnInput = func(ev ui.InputEvent) bool {
		if engine == database.BigQuery && ev.Kind == ui.InputText && ev.Text == "`" {
			sel := t.state.Selection()
			sql := t.state.Buffer.String()
			if sel.Anchor == sel.Head && len(t.state.Selections()) == 1 && len(sql) < completionSQLLimit {
				input := sql[:sel.Head] + "`" + sql[sel.Head:]
				parsed := sqlCompletionContext(input, sel.Head+1, engine)
				if !parsed.blocked && parsed.openBacktick {
					t.signatureDismissed = false
					t.state.Insert(ev.Text)
					a.completeSQL(t, editor.CompletionRequest{Offset: t.state.Cursor(), Version: t.state.Buffer.Version()})
					return true
				}
			}
		}
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape {
			t.cancelCompletion()
			t.signatureDismissed = true
		} else if ev.Kind == ui.InputText || ev.Kind == ui.InputKeyDown || ev.Kind == ui.InputPointerDown {
			t.signatureDismissed = false
		}
		return false
	}
	ui.Column(c.Key("sql-editor-area")).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		editor.View(c.Key(fmt.Sprintf("sql-editor-%p-%d", a, t.id)), &t.state).Grow(1).MinHeight(0).MinWidth(0)
		if t.state.Focused() {
			if help := t.functionHelp(engine); help != nil {
				functionHelpView(c, k, help)
			}
		}
	})
	a.query = t.state.Buffer.String()
}

// sqlLineComment reports a line-comment start at i. MySQL only treats "--" as
// a comment before whitespace; elsewhere it is subtraction.
func sqlLineComment(sql string, i int, engine database.Engine) bool {
	if strings.HasPrefix(sql[i:], "--") {
		if engine != database.MySQL || i+2 >= len(sql) {
			return true
		}
		switch sql[i+2] {
		case ' ', '\t', '\r', '\n':
			return true
		}
		return false
	}
	return (engine == database.BigQuery || engine == database.MySQL) && sql[i] == '#'
}

// statementRange ignores delimiters inside SQL strings, identifiers, comments,
// PostgreSQL dollar strings and BigQuery triple-quoted strings. Offsets are bytes.
func statementRange(sql string, caret int, engine database.Engine) (int, int) {
	caret = max(0, min(caret, len(sql)))
	start := 0
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
		if sql[i] == ';' {
			if caret <= i {
				return start, i + 1
			}
			start = i + 1
		}
		i++
	}
	// A caret just after the last semicolon still targets the preceding statement.
	if strings.TrimSpace(sql[start:]) == "" && start > 0 {
		return statementRange(sql, start-1, engine)
	}
	return start, len(sql)
}

func (a *App) explain() {
	if a.client.Engine() == database.BigQuery {
		a.status = "BigQuery does not support EXPLAIN statements"
		return
	}
	a.runAction(a.querySQL(), false, "explain")
}

func (a *App) runExplain(sql string) {
	prefix := "EXPLAIN "
	if a.client.Engine() == database.SQLite {
		prefix = "EXPLAIN QUERY PLAN "
	}
	a.runSQL(prefix+sql, false)
}
