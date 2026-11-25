package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dibiex/internal/database"
	"dibiex/internal/resultexport"
	"github.com/egoist/mygo/ui"
)

func TestExecuteToFileCapturesSQLWithoutRunningOrCreatingResults(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	query := "SELECT id, value FROM export_later ORDER BY id;"
	a := &App{client: client, connection: "Original", hideResults: true, hideSearch: true, explorerWidth: 260, editorPercent: 60, executionLine: 27}
	a.addQuery("SELECT 'first';\n" + query)
	tab := a.currentEditor()
	tab.state.Select(len("SELECT 'first';\n"), len("SELECT 'first';\n"))
	a.executionLine = 27
	v := ui.NewTester(a.view, 1180, 780)
	v.SetDark(true)
	if err = v.Click("Execute to File"); err != nil {
		t.Fatal(err)
	}
	f := a.resultExport
	if f == nil || !f.direct || f.data.SQL != query || f.client != client {
		t.Fatalf("wrong captured export: %+v", f)
	}
	if len(f.data.Columns) != 0 || len(f.data.Rows) != 0 || f.busy || f.errorText != "" {
		t.Fatal("opening export must not run a discovery/preview query")
	}
	if !a.hideResults || len(tab.execution.results) != 0 || tab.execution.running || a.executionLine != 27 {
		t.Fatal("opening export changed grid execution state")
	}
	if !v.HasText("Query to execute") || !v.HasText(query) {
		t.Fatal("missing captured SQL preview")
	}
	if _, ok := v.Find("Export row scope"); ok {
		t.Fatal("direct query export offered nonexistent loaded rows")
	}
	if _, ok := v.Find("Copy to Clipboard"); ok {
		t.Fatal("direct-to-file query export offered clipboard instead of streaming")
	}
	for _, width := range []int{1180, 800} {
		v.SetSize(width, 560)
		for _, label := range []string{"Execute to File dialog", "Start file export", "Cancel result export"} {
			box, ok := v.Find(label)
			if !ok || box.X < 0 || box.Y < 0 || box.X+box.W > float32(width)+1 || box.Y+box.H > 561 {
				t.Fatalf("clipped direct-export control at %d: %s %+v", width, label, box)
			}
		}
	}
	// The source does not exist until after the dialog opens: column discovery
	// must happen within the same execution that streams to the file.
	if _, err = client.Execute(ctx, `CREATE TABLE export_later(id INTEGER, value TEXT);
WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2505) INSERT INTO export_later SELECT i, 'value-' || i FROM n;`); err != nil {
		t.Fatal(err)
	}
	a.setQuery("SELECT 'edited after opening'")
	a.client = nil
	var out bytes.Buffer
	count, err := streamResultExport(ctx, f.client, f.data, f.sourceColumns, f.columns, f.filter, f.filterScope, func(d resultexport.Data) error { return resultexport.Write(ctx, &out, d, f.options) }, nil)
	if err != nil || count != 2505 {
		t.Fatalf("direct execution failed: %d %v", count, err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2506 || !reflect.DeepEqual(rows[0], []string{"id", "value"}) || !reflect.DeepEqual(rows[2505], []string{"2505", "value-2505"}) {
		t.Fatal("direct export did not discover schema and export all rows")
	}
	if len(tab.execution.results) != 0 || len(a.result.Rows) != 0 {
		t.Fatal("direct export populated grid results")
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		v.SetSize(1180, 780)
		file, err := os.Create(filepath.Join(dir, "execute-to-file-dark.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, v.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecuteToFileSelectionAndExistingResultsRemainIndependent(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Owner", hideResults: false}
	a.addQuery("SELECT 7 AS before;\nSELECT 8 AS chosen;\nSELECT 9 AS after;")
	tab := a.currentEditor()
	a.finishTabQuery(tab, database.Result{Columns: []string{"old"}, Rows: [][]string{{"keep"}}}, nil, "SELECT 'keep'", false, 1, 0, nil)
	retained := tab.execution.activeResult
	retained.pinned = true
	start := strings.Index(tab.state.Buffer.String(), "SELECT 8")
	tab.state.Select(start, start+len("SELECT 8 AS chosen;"))
	a.openQueryExport()
	f := a.resultExport
	if f.data.SQL != "SELECT 8 AS chosen;" {
		t.Fatal("export ignored editor selection")
	}
	var out bytes.Buffer
	count, err := streamResultExport(ctx, f.client, f.data, nil, nil, "", "", func(d resultexport.Data) error { return resultexport.Write(ctx, &out, d, f.options) }, nil)
	if err != nil || count != 1 || !strings.Contains(out.String(), "chosen\n8\n") {
		t.Fatalf("selection export: %q %v", out.String(), err)
	}
	if len(tab.execution.results) != 1 || tab.execution.activeResult != retained || !retained.pinned || a.result.Rows[0][0] != "keep" {
		t.Fatal("direct export changed pinned results")
	}
}

func TestDirectExportValidatesOptionsWithoutRequiringKnownColumns(t *testing.T) {
	o := resultexport.Options{Format: "CSV", Compression: "None", Separator: ","}
	if err := o.ValidateOptions(); err != nil {
		t.Fatal(err)
	}
	if o.Validate(resultexport.Data{}) == nil {
		t.Fatal("writer allowed a missing runtime schema")
	}
	o.Separator = "two"
	if o.ValidateOptions() == nil {
		t.Fatal("invalid separator can reach the query execution")
	}
	a := &App{}
	a.openQueryExport()
	if a.resultExport != nil {
		t.Fatal("disconnected console opened direct export")
	}
}

func TestExecuteToFileShortcutAndMenu(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Local", hideResults: true, hideSearch: true, explorerWidth: 260, editorPercent: 60}
	a.addQuery("SELECT 1 AS value")
	v := ui.NewTester(a.view, 800, 560)
	for _, label := range []string{"Execute", "Execute to File", "Explain", "Stop"} {
		box, ok := v.Find(label)
		if !ok || box.X < 0 || box.X+box.W > 801 {
			t.Fatalf("toolbar control clipped: %s %+v", label, box)
		}
	}
	v.Key(ui.Cmd|ui.Shift, ui.KeyEnter)
	v.Frame()
	if !a.showResultExport || a.resultExport == nil || !a.resultExport.direct {
		t.Fatal("shortcut did not open direct export")
	}
	if err = v.Click("Cancel result export"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Main menu"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Execute to File…"); err != nil {
		t.Fatal(err)
	}
	if !a.showResultExport || !a.resultExport.direct || a.resultExport.data.SQL != "SELECT 1 AS value" {
		t.Fatal("menu did not capture SQL for direct export")
	}
}
