package app

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestPinnedResultsPreserveDataAndPagination(t *testing.T) {
	a := &App{explorerWidth: 260, editorPercent: 60}
	a.addQuery("SELECT 1")
	tab := a.currentEditor()
	first := database.Result{Columns: []string{"value"}, Rows: [][]string{{"first"}}, Elapsed: time.Millisecond}
	a.finishTabQuery(tab, first, nil, "SELECT 1", false, 1, time.Millisecond, nil)
	a.page, a.selected = 2, 12
	a.storeQueryView(tab)
	pinned := tab.execution.activeResult
	pinned.pinned = true
	if !a.prepareResultTab(tab) {
		t.Fatal("could not allocate a second result")
	}
	a.loadQueryView(tab)
	second := database.Result{Columns: []string{"value"}, Rows: [][]string{{"second"}}, Elapsed: 2 * time.Millisecond}
	a.finishTabQuery(tab, second, nil, "SELECT 2", false, 1, 2*time.Millisecond, nil)
	if len(tab.execution.results) != 2 || pinned.result.Rows[0][0] != "first" || a.result.Rows[0][0] != "second" {
		t.Fatal("new execution overwrote its pinned predecessor")
	}
	a.selectResultTab(tab, pinned)
	if a.page != 2 || a.selected != 12 || a.result.Rows[0][0] != "first" {
		t.Fatal("pinned result lost its pagination or selection")
	}
	latest := tab.execution.results[1]
	a.selectResultTab(tab, latest)
	if !a.prepareResultTab(tab) {
		t.Fatal("could not reuse unpinned result")
	}
	a.finishTabQuery(tab, database.Result{Columns: []string{"value"}, Rows: [][]string{{"third"}}}, nil, "SELECT 3", false, 1, time.Millisecond, nil)
	if len(tab.execution.results) != 2 || latest.result.Rows[0][0] != "third" || pinned.result.Rows[0][0] != "first" {
		t.Fatal("unpinned result was not reused independently")
	}
	if !a.prepareResultTab(tab) {
		t.Fatal("could not prepare failed execution")
	}
	a.finishTabQuery(tab, database.Result{}, context.Canceled, "SELECT 4", false, 1, time.Second, nil)
	if !a.output || pinned.result.Rows[0][0] != "first" || pinned.result.Elapsed != time.Millisecond {
		t.Fatal("failed execution changed a pinned result")
	}
	a.selectResultTab(tab, pinned)
	a.closeResultTab(tab, pinned)
	if len(tab.execution.results) != 1 || a.result.Rows[0][0] != "third" {
		t.Fatal("closing a pinned result damaged another result")
	}
}

func TestResultStripOutputFirstAndPinControl(t *testing.T) {
	a := &App{explorerWidth: 260, editorPercent: 60}
	a.addQuery("SELECT 1")
	tab := a.currentEditor()
	result := database.Result{Columns: []string{"value"}, Rows: [][]string{{"first"}}}
	a.finishTabQuery(tab, result, nil, "SELECT 1", false, 1, time.Millisecond, nil)
	v := ui.NewTester(a.view, 1180, 780)
	v.SetDark(true)
	if err := v.Click("Pin Result 1"); err != nil {
		t.Fatal(err)
	}
	if !tab.execution.results[0].pinned {
		t.Fatal("pin button did not pin the result")
	}
	if !a.prepareResultTab(tab) {
		t.Fatal("second result not allocated")
	}
	a.loadQueryView(tab)
	a.finishTabQuery(tab, result, nil, "SELECT 2", false, 1, time.Millisecond, nil)
	v.Frame()
	output, _ := v.Find("Output")
	first, _ := v.Find("Result 1")
	second, ok := v.Find("Result 2")
	if !ok || output.X >= first.X || first.X >= second.X {
		t.Fatalf("wrong tab order: output=%+v first=%+v second=%+v", output, first, second)
	}
	if err := v.Click("Result 1"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Unpin Result 1"); err != nil {
		t.Fatal(err)
	}
	if tab.execution.results[0].pinned {
		t.Fatal("unpin button did not unpin")
	}
	if err := v.Click("Pin Result 1"); err != nil {
		t.Fatal(err)
	}
	pinned := tab.execution.results[0]
	v.Move(2, 2)
	before, _ := v.Find("Result 1")
	if _, ok := v.Find("Close Result 1"); !ok {
		t.Fatal("result tab must keep its close button visible")
	}
	pinControl, ok := v.Find("Unpin Result 1")
	if !ok || pinControl.Y <= before.Y+before.H {
		t.Fatal("pin action must remain in the toolbar below the result tabs")
	}
	if output := os.Getenv("DBCRAFT_SCREENSHOTS"); output != "" {
		file, err := os.Create(filepath.Join(output, "result-toolbar.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(file, v.Image()); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	v.Move(before.X+before.W/2, before.Y+before.H/2)
	after, _ := v.Find("Result 1")
	if before.W != after.W {
		t.Fatal("hover created a separate close-button space")
	}
	if err := v.Click("Close Result 1"); err != nil {
		t.Fatal(err)
	}
	if len(tab.execution.results) != 1 || tab.execution.results[0].id != 2 || !pinned.pinned {
		t.Fatal("hover action did not close the pinned result independently")
	}
}

func TestQueryCompletionKeepsSelectedPinnedResult(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT 1")
	tab := a.currentEditor()
	result := database.Result{Columns: []string{"value"}, Rows: [][]string{{"first"}}}
	a.finishTabQuery(tab, result, nil, "SELECT 1", false, 1, time.Millisecond, nil)
	pinned := tab.execution.activeResult
	pinned.pinned = true
	a.prepareResultTab(tab)
	a.loadQueryView(tab)
	pending := tab.execution.pendingResult
	tab.execution.running = true
	a.selectResultTab(tab, pinned)
	a.finishTabQuery(tab, database.Result{Columns: []string{"value"}, Rows: [][]string{{"second"}}}, nil, "SELECT 2", false, 1, time.Millisecond, nil)
	if a.result.Rows[0][0] != "first" || pinned.result.Rows[0][0] != "first" || pending.result.Rows[0][0] != "second" {
		t.Fatal("completion replaced the selected pinned result")
	}
}

func TestPinnedResultHistoryBound(t *testing.T) {
	a := &App{}
	a.addQuery("SELECT 1")
	tab := a.currentEditor()
	for range maxResultTabs {
		a.finishTabQuery(tab, database.Result{Columns: []string{"value"}, Rows: [][]string{{"retained"}}}, nil, "SELECT 1", false, 1, time.Millisecond, nil)
		tab.execution.activeResult.pinned = true
	}
	if a.prepareResultTab(tab) || len(tab.execution.results) != maxResultTabs {
		t.Fatal("retained-result bound was not enforced")
	}
	first := tab.execution.results[0]
	a.closeResultTab(tab, first)
	if !a.prepareResultTab(tab) || len(tab.execution.results) != maxResultTabs {
		t.Fatal("closing a retained result did not free capacity")
	}
}
