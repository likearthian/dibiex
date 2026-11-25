package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func completionFixture() ([]database.Schema, map[string]database.TableMetadata) {
	users := database.Table{Schema: "public", Name: "users", Kind: "table"}
	orders := database.Table{Schema: "public", Name: "orders", Kind: "table"}
	return []database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{users, orders}}}, map[string]database.TableMetadata{
		users.ID():  {Columns: []database.Column{{Name: "id", Type: "integer"}, {Name: "display name", Type: "text"}, {Name: "étiquette", Type: "text"}}},
		orders.ID(): {Columns: []database.Column{{Name: "order_id", Type: "integer"}}},
	}
}

func completionsAt(sql string, engine database.Engine, schemas []database.Schema, metadata map[string]database.TableMetadata) ([]editor.Completion, []database.Table) {
	offset := strings.IndexByte(sql, '|')
	sql = strings.Replace(sql, "|", "", 1)
	return sqlCompletions(sqlCompletionContext(sql, offset, engine), engine, schemas, metadata)
}

func findCompletion(items []editor.Completion, label string) *editor.Completion {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

func TestSQLCompletionContext(t *testing.T) {
	schemas, metadata := completionFixture()
	for _, tc := range []struct{ sql, want, insert, absent string }{
		{"sel|", "SELECT", "SELECT", ""},
		{"SELECT * FROM us|", "users", `public.users`, "id"},
		{"SELECT * FROM public.us|", "users", `users`, "COUNT"},
		{"SELECT users.|", "id", `id`, "order_id"},
		{"SELECT u.| FROM public.users AS u", "id", `id`, "order_id"},
		{"SELECT u.di| FROM users u JOIN orders o ON u.id=o.order_id", "display name", `"display name"`, "order_id"},
		{"SELECT o.| FROM users u JOIN orders o ON u.id=o.order_id", "order_id", `order_id`, "display name"},
		{"SELECT | FROM users", "display name", `"display name"`, "order_id"},
		{"SELECT * FROM users WHERE u.|", "", "", "order_id"},
		{"SELECT * FROM users, ord|", "orders", `public.orders`, "id"},
		{"SELECT id FROM orders; SELECT u.| FROM users u", "id", `id`, "order_id"},
		{"SELECT \"public\".\"users\".|", "id", `id`, "order_id"},
		{"SELECT u.é| FROM users u", "étiquette", `"étiquette"`, "id"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			items, missing := completionsAt(tc.sql, database.PostgreSQL, schemas, metadata)
			if len(missing) != 0 {
				t.Fatalf("unexpected missing metadata: %v", missing)
			}
			if tc.want != "" {
				got := findCompletion(items, tc.want)
				if got == nil || got.Insert != tc.insert {
					t.Fatalf("wanted %s -> %s; got %+v", tc.want, tc.insert, items)
				}
			}
			if tc.absent != "" && findCompletion(items, tc.absent) != nil {
				t.Fatalf("unexpected %s", tc.absent)
			}
		})
	}
}

func TestSQLCompletionIgnoresLiteralsAndComments(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		engine database.Engine
	}{
		{"SELECT 'sel|", database.SQLite},
		{"SELECT 'it''s sel|", database.PostgreSQL},
		{"SELECT E'it\\'s sel|", database.PostgreSQL},
		{"-- sel|", database.PostgreSQL},
		{"SELECT 1 /* outer /* nested */ sel|", database.PostgreSQL},
		{"DO $body$ SELECT sel|", database.PostgreSQL},
		{"SELECT '''text\nsel|", database.BigQuery},
		{"# sel|", database.BigQuery},
		{"SELECT \"sel|", database.PostgreSQL},
		{"SELECT [sel|", database.SQLite},
		{"SELECT 'it\\'s sel|", database.MySQL},
		{"SELECT \"sel|", database.MySQL},
		{"# sel|", database.MySQL},
	} {
		items, missing := completionsAt(tc.sql, tc.engine, nil, nil)
		if len(items) != 0 || len(missing) != 0 {
			t.Fatalf("completions inside %q: %+v", tc.sql, items)
		}
	}
	for _, sql := range []string{"SELECT ';'; -- ignored\nsel|", "/* comment */ sel|", "SELECT $$FROM fake$$; sel|"} {
		items, _ := completionsAt(sql, database.PostgreSQL, nil, nil)
		if findCompletion(items, "SELECT") == nil {
			t.Fatalf("missing keyword after closed literal/comment: %q", sql)
		}
	}
}

func TestSQLCompletionDialectsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		engine       database.Engine
		word, absent string
	}{
		{database.SQLite, "PRAGMA", "ILIKE"},
		{database.PostgreSQL, "ILIKE", "QUALIFY"},
		{database.MySQL, "AUTO_INCREMENT", "QUALIFY"},
		{database.BigQuery, "QUALIFY", "PRAGMA"},
	} {
		items, _ := completionsAt("|", tc.engine, nil, nil)
		if findCompletion(items, tc.word) == nil || findCompletion(items, tc.absent) != nil {
			t.Fatalf("wrong dialect %s: %v", tc.engine, items)
		}
	}
	schema := database.Schema{Name: "public", Loaded: true}
	for i := 0; i < completionLimit*2; i++ {
		schema.Tables = append(schema.Tables, database.Table{Schema: "public", Name: fmt.Sprintf("table_%04d", i)})
	}
	items, _ := completionsAt("SELECT * FROM |", database.PostgreSQL, []database.Schema{schema}, nil)
	if len(items) > completionLimit {
		t.Fatalf("unbounded candidates: %d", len(items))
	}
	items, _ = completionsAt("SELECT * FROM table_090|", database.PostgreSQL, []database.Schema{schema}, nil)
	if findCompletion(items, "table_0900") == nil {
		t.Fatal("limit excluded a matching table")
	}
	c := sqlCompletionContext(strings.Repeat("x", completionSQLLimit+1), 1, database.SQLite)
	if !c.blocked {
		t.Fatal("large SQL should not block the UI with analysis")
	}
}

func TestSQLCompletionMissingMetadataAndNestedFields(t *testing.T) {
	schemas, _ := completionFixture()
	_, missing := completionsAt("SELECT u.| FROM users u", database.PostgreSQL, schemas, nil)
	if len(missing) != 1 || missing[0].Name != "users" {
		t.Fatalf("unexpected metadata requests: %v", missing)
	}
	table := database.Table{Project: "my-project", Schema: "analytics", Name: "events"}
	schemas = []database.Schema{{Project: table.Project, Name: table.Schema, Loaded: true, Tables: []database.Table{table}}}
	metadata := map[string]database.TableMetadata{table.ID(): {Columns: []database.Column{{Name: "payload", Fields: []database.Column{{Name: "city", Type: "STRING"}}}}}}
	items, _ := completionsAt("SELECT e.payload.c| FROM `my-project.analytics.events` e", database.BigQuery, schemas, metadata)
	if got := findCompletion(items, "city"); got == nil || got.Insert != "`city`" {
		t.Fatalf("missing nested field: %+v", items)
	}
	items, _ = completionsAt("SELECT * FROM ev|", database.BigQuery, schemas, metadata)
	if got := findCompletion(items, "events"); got == nil || got.Insert != "`my-project.analytics.events`" {
		t.Fatalf("wrong BQ path: %+v", items)
	}
	if got := completionIdentifier("a\"b", database.SQLite); got != `"a""b"` {
		t.Fatalf("unsafe identifier: %s", got)
	}
}

func TestSQLCompletionLazySchemaIsTargeted(t *testing.T) {
	schemas := []database.Schema{{Project: "other-project", Name: "analytics"}, {Project: "my-project", Name: "analytics"}, {Project: "my-project", Name: "unrelated"}}
	for _, sql := range []string{"SELECT * FROM my-project.analytics.|", "SELECT e.| FROM `my-project.analytics.events` e"} {
		// Project names with hyphens must be quoted in an identifier path.
		sql = strings.Replace(sql, "FROM my-project.analytics.", "FROM `my-project`.analytics.", 1)
		offset := strings.IndexByte(sql, '|')
		c := sqlCompletionContext(strings.Replace(sql, "|", "", 1), offset, database.BigQuery)
		s := completionMissingSchema(c, schemas)
		if s == nil || s.Project != "my-project" || s.Name != "analytics" {
			t.Fatalf("wrong targeted dataset for %q: %+v", sql, s)
		}
	}
	for _, sql := range []string{"sel|", "SELECT * FROM |", "SELECT * FROM unknown.|"} {
		offset := strings.IndexByte(sql, '|')
		c := sqlCompletionContext(strings.Replace(sql, "|", "", 1), offset, database.BigQuery)
		if s := completionMissingSchema(c, schemas); s != nil {
			t.Fatalf("unrequested discovery for %q: %+v", sql, s)
		}
	}
}

func TestSQLCompletionStaleRequests(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT u.")
	tab := a.currentEditor()
	tab.state.Select(len("SELECT u."), len("SELECT u."))
	r := editor.CompletionRequest{Offset: len("SELECT u."), Version: tab.state.Buffer.Version()}
	if !a.completionCurrent(tab, r, tab.completionID) {
		t.Fatal("current request rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	tab.completionCancel = cancel
	id := tab.completionID
	tab.cancelCompletion()
	if ctx.Err() == nil || a.completionCurrent(tab, r, id) {
		t.Fatal("cancellation failed")
	}
	id = tab.completionID
	tab.state.Buffer.Set("SELECT 2")
	if a.completionCurrent(tab, r, id) {
		t.Fatal("accepted edited buffer")
	}
	r.Version = tab.state.Buffer.Version()
	a.addQuery("other")
	if a.completionCurrent(tab, r, id) {
		t.Fatal("accepted inactive tab")
	}
}

func TestSQLCompletionNativeEditor(t *testing.T) {
	a := &App{}
	a.addQuery("")
	tab := a.currentEditor()
	v := ui.NewTester(func(c *ui.Context) { a.sqlEditor(c) }, 800, 400)
	if err := v.Click("SQL query editor"); err != nil {
		t.Fatal(err)
	}
	v.Type("sel")
	if !v.HasText("SELECT") {
		t.Fatalf("native popup not shown: %v", v.Texts())
	}
	v.Key(0, ui.KeyEnter)
	if got := tab.state.Buffer.String(); got != "SELECT" {
		t.Fatalf("completion inserted %q", got)
	}
	tab.state.Run(editor.Undo)
	if got := tab.state.Buffer.String(); got != "sel" {
		t.Fatalf("undo did not restore typed prefix: %q", got)
	}
	v.Frame()
	tab.state.Run(editor.ShowCompletions)
	v.Frame()
	if !v.HasText("SELECT") {
		t.Fatal("explicit completion did not open popup")
	}
	v.Key(0, ui.KeyEscape)
	if v.HasText("SELECT") {
		t.Fatal("Escape did not dismiss popup")
	}
}

func TestSQLCompletionLateColumnsWhileTyping(t *testing.T) {
	schemas, metadata := completionFixture()
	a := &App{schemas: schemas}
	a.addQuery("SELECT  FROM public.users")
	tab := a.currentEditor()
	tab.engine = database.PostgreSQL
	v := ui.NewTester(func(c *ui.Context) {
		tab.state.OnCompletion = func(r editor.CompletionRequest) { a.completeSQL(tab, r) }
		editor.View(c, &tab.state).Grow(1)
	}, 800, 400)
	if err := v.Click("SQL query editor"); err != nil {
		t.Fatal(err)
	}
	tab.state.Select(7, 7)
	v.Frame()
	v.Type("d")
	input := tab.state.Buffer.String()
	r := editor.CompletionRequest{Offset: tab.state.Selection().Head, Version: tab.state.Buffer.Version()}
	id := tab.completionID
	if !v.HasText("DISTINCT") {
		t.Fatalf("expected provisional keyword list: %v", v.Texts())
	}
	v.Type("i")
	if tab.completionID != id {
		t.Fatal("test did not exercise MyGo's cached completion list")
	}
	if r.Version == tab.state.Buffer.Version() {
		t.Fatal("test did not advance the editor version")
	}
	// The worker finishes after the user has narrowed the same prefix.
	a.metadata = metadata
	a.refreshSQLCompletions(tab, input, r, id, database.PostgreSQL)
	v.Frame()
	if !v.HasText("display name") {
		t.Fatalf("late column missing: %v", v.Texts())
	}
	if err := v.Click("display name"); err != nil {
		t.Fatal(err)
	}
	if got := tab.state.Buffer.String(); got != `SELECT "display name" FROM public.users` {
		t.Fatalf("wrong replacement: %q", got)
	}
}

func TestSQLCompletionLateColumnsStayDismissed(t *testing.T) {
	for _, action := range []string{"escape", "changed SQL", "different tab"} {
		t.Run(action, func(t *testing.T) {
			schemas, metadata := completionFixture()
			a := &App{schemas: schemas}
			a.addQuery("SELECT di FROM public.users")
			tab := a.currentEditor()
			v := ui.NewTester(func(c *ui.Context) { a.sqlEditor(c) }, 800, 400)
			if err := v.Click("SQL query editor"); err != nil {
				t.Fatal(err)
			}
			tab.state.Select(9, 9)
			v.Frame()
			input := tab.state.Buffer.String()
			r := editor.CompletionRequest{Offset: 9, Version: tab.state.Buffer.Version()}
			id := tab.completionID
			switch action {
			case "escape":
				v.Key(0, ui.KeyEscape)
			case "changed SQL":
				tab.state.Buffer.Set("SELECT di FROM public.orders")
				tab.state.Select(9, 9)
				v.Frame()
			case "different tab":
				a.addQuery("SELECT 1")
				v.Frame()
			}
			a.metadata = metadata
			a.refreshSQLCompletions(tab, input, r, id, database.PostgreSQL)
			v.Frame()
			if v.HasText("display name") {
				t.Fatal("stale metadata reopened column suggestions")
			}
		})
	}
}

func TestSQLCompletionColumnsSurviveLargeCatalog(t *testing.T) {
	schemas, metadata := completionFixture()
	for i := 0; i < completionLimit*2; i++ {
		schemas[0].Tables = append(schemas[0].Tables, database.Table{Schema: "public", Name: fmt.Sprintf("extra_%04d", i)})
	}
	items, _ := completionsAt("SELECT | FROM public.users", database.PostgreSQL, schemas, metadata)
	if findCompletion(items, "id") == nil || findCompletion(items, "display name") == nil {
		t.Fatal("table suggestions crowded out columns")
	}
	if len(items) == 0 || items[0].Kind != editor.CompletionField {
		t.Fatal("columns are not prioritized")
	}
}

func TestBigQueryProjectCompletion(t *testing.T) {
	for _, prefix := range []string{"sample", "sample-pro", "`", "`sample-pro"} {
		sql := "SELECT * FROM " + prefix
		context := sqlCompletionContext(sql, len(sql), database.BigQuery)
		items, _ := sqlCompletions(context, database.BigQuery, nil, nil, "sample-project")
		if context.start != len("SELECT * FROM ") || len(items) != 1 || items[0].Insert != "`sample-project`" {
			t.Fatalf("completion for %q starts at %d with items %+v", prefix, context.start, items)
		}
		if strings.HasPrefix(prefix, "`") && !strings.HasPrefix(items[0].FilterText, prefix) {
			t.Fatalf("native completion filter would hide %q for prefix %q", items[0].Label, prefix)
		}
	}
}
