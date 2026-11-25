package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"dibiex/internal/database"
)

func TestQueryTabsKeepIndependentExecutionsAndResults(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT 'first'")
	first := a.currentEditor()
	first.execution.running = true
	first.execution.started = time.Now()
	first.execution.status = "Running…"
	a.loadQueryView(first)
	a.addQuery("SELECT 'second'")
	second := a.currentEditor()
	if a.queryBusy() {
		t.Fatal("running query in another tab blocked execution")
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	second.execution.running = true
	second.execution.cancel = cancelSecond
	second.execution.status = "Running…"
	second.execution.line = 7
	a.loadQueryView(second)
	firstResult := database.Result{Columns: []string{"source"}, Rows: [][]string{{"first"}}, Elapsed: 20 * time.Millisecond}
	a.finishTabQuery(first, firstResult, nil, "SELECT 'first'", false, 1, 20*time.Millisecond, nil)
	if a.currentEditor() != second || !a.queryBusy() || len(a.result.Rows) != 0 || a.status != "Running…" {
		t.Fatal("background completion replaced the foreground tab's execution")
	}
	a.selectQueryTab(0)
	if a.queryBusy() || a.result.Rows[0][0] != "first" {
		t.Fatal("finished tab did not restore its own results")
	}
	a.page = 3
	a.selected = 9
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first.execution.running = true
	first.execution.cancel = cancelFirst
	a.selectQueryTab(1)
	a.stopQuery()
	if secondCtx.Err() == nil || firstCtx.Err() != nil {
		t.Fatal("Stop cancelled a query in another tab")
	}
	a.finishTabQuery(second, database.Result{}, context.Canceled, "SELECT 'second'", false, 7, time.Second, nil)
	if !a.output || !strings.Contains(a.errorText, "canceled") {
		t.Fatal("cancelled query's error did not stay in its own tab")
	}
	a.selectQueryTab(0)
	if a.page != 3 || a.selected != 9 || len(a.result.Rows) != 1 || a.result.Rows[0][0] != "first" || a.errorText != "" {
		t.Fatal("switching tabs lost pagination, selection, or results")
	}
	a.closeQueryTab()
	if firstCtx.Err() == nil || a.currentEditor() != second || len(a.result.Rows) != 0 || !a.output {
		t.Fatal("closing a running tab did not cancel only its execution and restore the next result")
	}
}
