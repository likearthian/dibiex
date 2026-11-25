package app

import (
	"encoding/csv"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestResultFilteringSortingAndCopy(t *testing.T) {
	r := database.Result{Columns: []string{"name", "note"}, Rows: [][]string{{"Beta", "NULL"}, {"alpha", "a,b\n\"quote\""}, {"Beta", "[BLOB: 4 bytes]"}}}
	v := resultView{filter: "beta", scope: "0", sort: ui.SortOrder{Column: "1", Descending: true}}
	if got := resultRowOrder(r, &v); !reflect.DeepEqual(got, []int{2, 0}) {
		t.Fatalf("filtered sort: %v", got)
	}
	v.filter, v.scope = "null", "1"
	if got := resultRowOrder(r, &v); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("column scope: %v", got)
	}
	v.scope = "0"
	if got := resultRowOrder(r, &v); len(got) != 0 {
		t.Fatalf("filter leaked to another column: %v", got)
	}
	text := resultDelimited(r, []int{1, 0, 2}, []int{1, 0}, ',')
	parsed, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	want := [][]string{{"note", "name"}, {"a,b\n\"quote\"", "alpha"}, {"NULL", "Beta"}, {"[BLOB: 4 bytes]", "Beta"}}
	if err != nil || !reflect.DeepEqual(parsed, want) {
		t.Fatalf("CSV lost data: %v %v", parsed, err)
	}
	if r.Rows[0][0] != "Beta" {
		t.Fatal("sorting mutated snapshot")
	}
}

func TestResultCellBlockUsesVisibleColumnOrder(t *testing.T) {
	v := resultView{}
	v.selection.Add(0)
	v.selection.Add(2)
	g := ui.ListState{Columns: ui.TableLayout{Order: []string{"2", "0", "1"}}, Cells: ui.ColumnRange{Anchor: "2", Cursor: "0"}}
	rows, cols := resultSelectedBlock(&g, &v, []int{4, 3, 2, 1}, []int{0, 2}, 0)
	if !reflect.DeepEqual(rows, []int{4, 2}) || !reflect.DeepEqual(cols, []int{2, 0}) {
		t.Fatalf("wrong block: %v %v", rows, cols)
	}
}

func TestResultPanelControlsAndRetainedState(t *testing.T) {
	ui.RegisterFont(monoFont, "JetBrains Mono")
	a := &App{explorerWidth: 260, editorPercent: 55, connection: "Neon", hideSearch: true}
	a.addQuery("SELECT * FROM cameras")
	tab := a.currentEditor()
	r := database.Result{Columns: []string{"id", "site_id", "zone", "health"}, ColumnTypes: []string{"TEXT", "TEXT", "TEXT", "TEXT"}, Rows: [][]string{{"cam-04", "site-1", "Dockside", "online"}, {"cam-07", "site-1", "Gate", "online"}, {"cam-home", "site-2", "Main Hall", "offline"}}, Elapsed: 11 * time.Millisecond}
	a.finishTabQuery(tab, r, nil, "SELECT * FROM cameras", false, 1, r.Elapsed, nil)
	tester := ui.NewTester(a.view, 1180, 780)
	tester.SetDark(true)
	for _, label := range []string{"Filter loaded rows", "Show result SQL"} {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	first := tab.execution.activeResult
	first.view.filter = "offline"
	tester.Frame()
	if !tester.HasText("cam-home") || tester.HasText("cam-04") {
		t.Fatal("filter did not restrict grid rows")
	}
	first.pinned = true
	a.prepareResultTab(tab)
	a.loadQueryView(tab)
	a.finishTabQuery(tab, r, nil, "SELECT 2", false, 1, r.Elapsed, nil)
	if tab.execution.activeResult.view.filter != "" {
		t.Fatal("new result inherited another result's filter")
	}
	a.selectResultTab(tab, first)
	if first.view.filter != "offline" || !first.view.showSQL {
		t.Fatal("result lost its presentation state")
	}
	first.view.filter, first.view.showSQL, first.view.showFilter = "", false, false
	tester.Frame()
	first.view.selection.Add(1)
	if err := tester.Click("health"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if first.view.sort.Column != "3" || first.view.selection.Len() != 0 {
		t.Fatal("header sort did not clear stale cell selection")
	}
	if got := resultRowOrder(r, &first.view); !reflect.DeepEqual(got, []int{2, 0, 1}) {
		t.Fatalf("header sort order: %v", got)
	}
	for _, width := range []int{1180, 800} {
		tester.SetSize(width, 780)
		for _, label := range []string{"Export Data", "Result columns", "Next results page", "Hide results · Alt+8"} {
			box, ok := tester.Find(label)
			if !ok || box.X < 0 || box.X+box.W > float32(width)+1 {
				t.Fatalf("control outside %d: %s %+v", width, label, box)
			}
		}
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		tester.SetSize(1180, 780)
		f, err := os.Create(filepath.Join(dir, "result-panel-dark.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, tester.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
