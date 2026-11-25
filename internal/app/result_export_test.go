package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dbcraft/internal/database"
	"dbcraft/internal/resultexport"
	"github.com/egoist/mygo/ui"
)

func TestExportDialogUsesFilteredSnapshotAndFormatOptions(t *testing.T) {
	ui.RegisterFont(monoFont, "JetBrains Mono")
	a := &App{connection: "Neon", explorerWidth: 260, editorPercent: 55, hideSearch: true}
	a.addQuery("SELECT id, note FROM cameras")
	tab := a.currentEditor()
	r := database.Result{Columns: []string{"id", "note", "hidden"}, Rows: [][]string{{"001", "Dockside", "secret"}, {"002", "Gate", "other"}}}
	a.finishTabQuery(tab, r, nil, "SELECT id, note FROM cameras", false, 1, time.Millisecond, nil)
	snapshot := tab.execution.activeResult
	snapshot.view.hidden = map[int]bool{2: true}
	snapshot.view.filter = "Dockside"
	a.grid.Columns = ui.TableLayout{Order: []string{"1", "0", "2"}}
	v := ui.NewTester(a.view, 1180, 780)
	v.SetDark(true)
	if _, ok := v.Find("Copy TSV"); ok {
		t.Fatal("separate TSV button remains")
	}
	if _, ok := v.Find("Copy CSV"); ok {
		t.Fatal("separate CSV button remains")
	}
	if err := v.Click("Export Data"); err != nil {
		t.Fatal(err)
	}
	f := a.resultExport
	if f == nil || !reflect.DeepEqual(f.data.Columns, []string{"note", "id"}) || !reflect.DeepEqual(f.data.Rows, [][]string{{"Dockside", "001"}}) {
		t.Fatalf("wrong export snapshot: %+v", f)
	}
	if f.rowScope != exportLoadedRows {
		t.Fatal("exporting from the grid must default to loaded rows")
	}
	if !v.HasText("1 loaded rows · 2 visible columns · Filter: Dockside") {
		t.Fatal("export summary must describe the filtered snapshot")
	}
	if err := v.Click("Advanced options"); err != nil {
		t.Fatal(err)
	}
	// Reruns and tab navigation must not retarget an already-open export.
	a.result.Rows = [][]string{{"new", "different", "value"}}
	if f.data.Rows[0][0] != "Dockside" {
		t.Fatal("export snapshot changed")
	}
	if err := v.Click("Include CSV header"); err != nil {
		t.Fatal(err)
	}
	if f.options.Header {
		t.Fatal("header option did not change")
	}
	f.path = filepath.Join("C:\\exports", "result.csv")
	if err := v.Click("Export format"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Excel (xlsx)"); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if f.options.Format != "Excel (xlsx)" || f.options.Compression != "ZIP" || !strings.HasSuffix(f.path, ".xlsx") {
		t.Fatalf("format change did not normalize options: %+v", f)
	}
	if _, ok := v.Find("Output query to separate sheet"); !ok {
		t.Fatal("missing query worksheet option")
	}
	if !v.HasText("Binary format · text preview is not available.") {
		t.Fatal("missing binary preview explanation")
	}
	for _, width := range []int{1180, 800} {
		v.SetSize(width, 560)
		for _, label := range []string{"Export Data dialog", "Export to File", "Cancel result export", "Copy to Clipboard"} {
			box, ok := v.Find(label)
			if !ok || box.X < 0 || box.Y < 0 || box.X+box.W > float32(width)+1 || box.Y+box.H > 561 {
				t.Fatalf("export control clipped at %d: %s %+v", width, label, box)
			}
		}
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		v.SetSize(1180, 780)
		for _, format := range []string{"CSV", "Excel (xlsx)", "Parquet"} {
			f.options.Format = format
			v.Frame()
			file, err := os.Create(filepath.Join(dir, "export-"+f.options.Extension()+"-dark.png"))
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
}

func TestFullResultExportUsesOriginalQueryBeyondLoadedRows(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	query := `WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2505) SELECT i AS id, CASE WHEN i%2=0 THEN 'even' ELSE 'odd' END AS kind FROM n ORDER BY i`
	result, err := client.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1000 || !result.Truncated {
		t.Fatal("test did not exercise the grid limit")
	}
	a := &App{client: client, connection: "original"}
	a.addQuery(query)
	tab := a.currentEditor()
	a.finishTabQuery(tab, result, nil, query, false, 1, time.Millisecond, nil)
	snapshot := tab.execution.activeResult
	snapshot.view.filter, snapshot.view.scope = "even", "1"
	rows := resultRowOrder(result, &snapshot.view)
	a.openResultExport(snapshot, rows, []int{1, 0})
	f := a.resultExport
	a.client = nil
	a.query = "SELECT 'changed editor query'"
	path := filepath.Join(t.TempDir(), "all.csv")
	count, err := streamResultExport(ctx, f.client, f.data, f.sourceColumns, f.columns, f.filter, f.filterScope, func(d resultexport.Data) error {
		return resultexport.Save(ctx, path, false, d, f.options)
	}, nil)
	if err != nil || count != 1252 {
		t.Fatalf("full query did not stream every matching row: %d %v", count, err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	exported, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(exported) != 1253 || !reflect.DeepEqual(exported[1252], []string{"even", "2504"}) {
		t.Fatal("full export stopped at loaded rows or changed column order")
	}
	if len(snapshot.result.Rows) != 1000 || len(f.data.Rows) != 500 {
		t.Fatal("full export expanded the grid or changed its preview")
	}
	limitQuery := query + " LIMIT 1234"
	var out bytes.Buffer
	count, err = streamResultExport(ctx, f.client, resultexport.Data{Columns: []string{"id", "kind"}, SQL: limitQuery}, f.sourceColumns, []int{0, 1}, "", "", func(d resultexport.Data) error { return resultexport.Write(ctx, &out, d, f.options) }, nil)
	if err != nil || count != 1234 {
		t.Fatalf("export did not respect SQL LIMIT: %d %v", count, err)
	}
}

func TestExportPreviewIsBoundedAndExtensionsTrackCompression(t *testing.T) {
	var writer exportPreviewWriter
	_, err := io.Copy(&writer, bytes.NewBufferString(strings.Repeat("x", 70000)))
	if err != io.ErrShortWrite || writer.Len() != 65536 {
		t.Fatalf("unbounded preview: %d %v", writer.Len(), err)
	}
	f := resultExportForm{separator: "Tab"}
	f.options.Format = "CSV"
	f.options.Compression = "None"
	f.path = "result.csv"
	f.syncOptions()
	if f.options.Separator != "\t" {
		t.Fatal("tab separator not applied")
	}
	f.options.Compression = "GZIP"
	f.syncOptions()
	if f.path != "result.csv.gz" {
		t.Fatalf("wrong gzip suffix: %s", f.path)
	}
	f.options.Format = "Parquet"
	f.syncOptions()
	if f.path != "result.parquet" || f.options.Compression != "Snappy" {
		t.Fatal("format retained invalid compression or suffix")
	}
}
