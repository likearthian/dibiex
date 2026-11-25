package app

import (
	"context"
	"testing"
	"time"

	"dibiex/internal/database"
)

func TestPostgreSQLCursorEligibility(t *testing.T) {
	for _, query := range []string{"SELECT * FROM records;", "-- comment\n SELECT ';'", "/* outer /* inner */ */ WITH x AS (SELECT 1) SELECT * FROM x", "VALUES (1),(2)", "SELECT $$a;b$$"} {
		if !postgresCursorQuery(query) {
			t.Fatalf("eligible query skipped: %s", query)
		}
	}
	for _, query := range []string{"", "-- comment", "/* unfinished", "BEGIN", "COMMIT", "DELETE FROM records", "EXPLAIN SELECT 1", "SELECT 1; SELECT 2", "SELECT 1; COMMIT;"} {
		if postgresCursorQuery(query) {
			t.Fatalf("script/write/transaction routed to cursor: %s", query)
		}
	}
}

func TestCursorFetchKeepsSelectedPinnedResultAndGrid(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT 1")
	tab := a.currentEditor()
	previous := database.Result{Columns: []string{"id"}, Rows: [][]string{{"1"}}, More: true, Elapsed: time.Millisecond}
	a.finishTabQuery(tab, previous, nil, "SELECT 1", false, 1, time.Millisecond, nil)
	target := tab.execution.activeResult
	target.pinned = true
	if !a.prepareResultTab(tab) {
		t.Fatal("failed to prepare second result")
	}
	pinned := tab.execution.activeResult
	a.finishTabQuery(tab, database.Result{Columns: []string{"id"}, Rows: [][]string{{"pinned"}}}, nil, "SELECT 'pinned'", false, 1, time.Millisecond, nil)
	target.pinned = false
	pinned.pinned = true
	a.page, a.selected = 3, 7
	a.storeQueryView(tab)
	tab.execution.running = true
	tab.execution.pendingResult = target
	a.finishCursorFetch(tab, target, previous, database.Result{Rows: [][]string{{"2"}}, More: true}, nil)
	if a.result.Rows[0][0] != "pinned" || a.page != 3 || a.selected != 7 || len(pinned.result.Rows) != 1 {
		t.Fatal("fetch overwrote the selected pinned result or grid")
	}
	if len(target.result.Rows) != 2 || target.result.Rows[1][0] != "2" || len(previous.Rows) != 1 {
		t.Fatal("batch not appended independently")
	}
	a.selectResultTab(tab, target)
	a.page, a.selected = 0, 1
	a.finishCursorFetch(tab, target, target.result, database.Result{Rows: [][]string{{"3"}}}, nil)
	if len(a.result.Rows) != 3 || a.selected != 1 || a.page != 0 || a.result.More {
		t.Fatal("fetch lost active result state")
	}
	a.finishCursorFetch(tab, target, target.result, database.Result{}, context.Canceled)
	if len(a.result.Rows) != 3 || !a.output || tab.execution.running {
		t.Fatal("cancelled fetch discarded already loaded rows")
	}
}

func TestCursorFetchFinishesInOwningTab(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT 1")
	first := a.currentEditor()
	first.cursorFetch = true
	r := database.Result{Columns: []string{"id"}, Rows: [][]string{{"1"}}, More: true}
	a.finishTabQuery(first, r, nil, "SELECT 1", false, 1, 0, nil)
	target := first.execution.activeResult
	a.addQuery("SELECT 9")
	second := a.currentEditor()
	if second.cursorFetch {
		t.Fatal("cursor preference leaked into another tab")
	}
	a.finishTabQuery(second, database.Result{Columns: []string{"id"}, Rows: [][]string{{"9"}}}, nil, "SELECT 9", false, 1, 0, nil)
	a.finishCursorFetch(first, target, r, database.Result{Rows: [][]string{{"2"}}}, nil)
	if a.result.Rows[0][0] != "9" || len(target.result.Rows) != 2 {
		t.Fatal("background fetch retargeted the selected tab")
	}
}
