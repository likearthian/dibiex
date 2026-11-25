package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestStatementRange(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		engine database.Engine
	}{
		{"SELECT ';', 'it''s;ok';\nSELECT 2;", database.SQLite},
		{"SELECT 1 /* ; /* nested ; */ ; */;\nSELECT 2;", database.PostgreSQL},
		{"DO $body$ BEGIN RAISE NOTICE ';'; END $body$;\nSELECT 2;", database.PostgreSQL},
		{"SELECT '''multi;\nline''';\nSELECT 2;", database.BigQuery},
		{"SELECT [semi;column] FROM t;\nSELECT 2;", database.SQLite},
		{"SELECT 'it\\'s; still one';\nSELECT 2;", database.MySQL},
		{"# leading ; comment\nSELECT 1;\nSELECT 2;", database.MySQL},
		{"SELECT 5--3;\nSELECT 2;", database.MySQL},
	} {
		caret := strings.Index(tc.sql, "SELECT 2") + 2
		s, e := statementRange(tc.sql, caret, tc.engine)
		if strings.TrimSpace(tc.sql[s:e]) != "SELECT 2;" {
			t.Fatalf("wrong statement %q", tc.sql[s:e])
		}
		s, e = statementRange(tc.sql, 0, tc.engine)
		if strings.Contains(tc.sql[s:e], "SELECT 2") {
			t.Fatalf("first statement included second: %q", tc.sql[s:e])
		}
	}
	a := &App{query: "SELECT 'é';\nSELECT 2;"}
	a.addQuery(a.query)
	tab := a.currentEditor()
	tab.state.Select(0, len("SELECT 'é';"))
	if got := a.querySQL(); got != "SELECT 'é';" {
		t.Fatal(got)
	}
	a.addQuery("SELECT 3;")
	if a.querySQL() != "SELECT 3;" {
		t.Fatal("new tab lost its SQL")
	}
	a.activeTab = 0
	if a.querySQL() != "SELECT 'é';" {
		t.Fatal("tab selection was lost")
	}
}

func TestErrorLineHints(t *testing.T) {
	for _, tc := range []struct {
		err       error
		sql       string
		line, col int
	}{
		{fmt.Errorf("query: %w", &pgconn.PgError{Position: 13}), "SELECT 'é';\nBAD", 2, 1},
		{fmt.Errorf("Syntax error at [3:7]"), "", 3, 7},
		{fmt.Errorf("near BAD: syntax error"), "", 0, 0},
	} {
		line, col := queryErrorPosition(tc.err, tc.sql)
		if line != tc.line || col != tc.col {
			t.Fatalf("position: %d:%d, wanted %d:%d", line, col, tc.line, tc.col)
		}
	}
}

func TestSavedConnectionsExcludePasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections", "connections.json")
	connections := []savedConnection{{Name: "local", Config: database.Config{Engine: database.PostgreSQL, Host: "localhost", Password: "never-store-this"}}}
	if err := writeConnections(path, connections); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "never-store-this") {
		t.Fatal("password saved")
	}
	got, err := readConnections(path)
	if err != nil || len(got) != 1 || got[0].Config.Host != "localhost" || got[0].Config.Password != "" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if err = writeConnections(path, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIDEPaginationAndInspector(t *testing.T) {
	a := &App{query: "SELECT 1;", explorerWidth: 250, editorPercent: 55, showInspector: true, activeTable: "t", inspectorTable: database.Table{Name: "projects"}, metadata: map[string]database.TableMetadata{"\x00\x00projects": {Columns: []database.Column{{Name: "id", Type: "INTEGER", PrimaryKey: true}}, RowEstimate: "Unavailable", DDL: "CREATE TABLE projects (id INTEGER);"}}}
	a.result.Columns = []string{"value"}
	for i := 0; i < 205; i++ {
		a.result.Rows = append(a.result.Rows, []string{fmt.Sprintf("record-%03d", i)})
	}
	a.addQuery(a.query)
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Next results page"); err != nil {
		t.Fatal(err)
	}
	if a.page != 1 || !v.HasText("record-100") {
		t.Fatal("second page not displayed")
	}
	if err := v.Click("New query tab"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 2 {
		t.Fatal("query tab not added")
	}
	for _, size := range [][2]int{{1180, 780}, {800, 560}} {
		if size[0] < 1100 {
			a.showInspector = false
		}
		v.SetSize(size[0], size[1])
		for _, label := range []string{"Execute", "Explain", "Inspector", "Export Data"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
				t.Fatalf("%s clipped at %v: %+v", label, size, r)
			}
		}
		if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ide-%d.png", size[0])))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, v.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := v.Click("Inspector"); err != nil {
		t.Fatal(err)
	}
	if !a.showInspector || !v.HasText("Estimated rows") || !v.HasText("Unavailable") {
		t.Fatal("compact inspector drawer did not open")
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "inspector-drawer.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, v.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
