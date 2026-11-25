package app

import (
	"fmt"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestRightSidebarMinimizeClosesEntirePanel(t *testing.T) {
	for _, width := range []int{1472, 800} {
		for _, inspector := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/inspector=%t", width, inspector), func(t *testing.T) {
				a := &App{explorerWidth: 260, editorPercent: 60, hideResults: true, showFiles: true, showInspector: inspector, rightWidth: 320}
				title := "Files"
				if inspector {
					title = "Inspector"
				}
				if width < 1100 {
					a.showInspector, a.filesDrawer = true, !inspector
				}
				v := ui.NewTester(a.view, width, 884)
				if err := v.Click("Hide " + title); err != nil {
					t.Fatal(err)
				}
				v.Frame()
				if a.showInspector || a.showFiles || a.filesDrawer {
					t.Fatal("one minimize click must hide both right-pane views")
				}
				for _, size := range []int{800, 1472} {
					v.SetSize(size, 884)
					if _, ok := v.Find("Show Files"); ok {
						t.Fatal("minimized panel remained visible or reopened on resize")
					}
					if _, ok := v.Find("Hide Inspector"); ok {
						t.Fatal("minimized Inspector remained visible")
					}
				}
				if a.rightWidth != 320 {
					t.Fatal("minimizing changed the preferred panel width")
				}
				if err := v.Click("Toggle Files"); err != nil {
					t.Fatal(err)
				}
				if !v.HasText("Scratches and Consoles") {
					t.Fatal("Files could not be reopened after minimizing")
				}
			})
		}
	}
}

func TestRightSidebarViewSwitch(t *testing.T) {
	table := database.Table{Schema: "public", Name: "events", Kind: "Tables"}
	col := database.Column{Name: "created_at", Type: "timestamp", Default: "CURRENT_TIMESTAMP", Nullable: "NO"}
	a := &App{connection: "Local database", explorerWidth: 260, editorPercent: 60, hideResults: true, showFiles: true, rightWidth: 320}
	a.metadata = map[string]database.TableMetadata{table.ID(): {FetchedAt: time.Now(), Columns: []database.Column{col}}}
	v := ui.NewTester(a.view, 1472, 884)
	if err := v.Click("Show Inspector"); err != nil {
		t.Fatal(err)
	}
	if !a.showInspector || !v.HasText("Select an object") {
		t.Fatal("Inspector switch did not open the empty state")
	}
	a.inspectColumn(table, []string{col.Name}, col)
	v.Frame()
	if err := v.Click("Copy Default"); err != nil {
		t.Fatal(err)
	}
	if v.Clipboard() != col.Default {
		t.Fatal("SQL field copy did not copy the complete value")
	}
	for _, size := range [][2]int{{1472, 884}, {800, 560}} {
		v.SetSize(size[0], size[1])
		if err := v.Click("Show Files"); err != nil {
			t.Fatal(err)
		}
		if !v.HasText("Scratches and Consoles") {
			t.Fatal("Files switch did not show consoles")
		}
		if err := v.Click("Show Inspector"); err != nil {
			t.Fatal(err)
		}
		if a.inspectorColumn == nil || a.inspectorColumn.Column.Name != col.Name {
			t.Fatal("switching panes lost the inspected object")
		}
		for _, label := range []string{"Show Files", "Show Inspector", "Hide Inspector"} {
			r, ok := v.Find(label)
			if !ok || r.X < 0 || r.X+r.W > float32(size[0])+1 {
				t.Fatalf("sidebar header clipped at %v: %s %+v", size, label, r)
			}
		}
	}
	if err := v.Click("Show Files"); err != nil {
		t.Fatal(err)
	}
	v.SetSize(1472, 884)
	if a.showInspector || !a.showFiles || !v.HasText("Scratches and Consoles") {
		t.Fatal("Files drawer did not return to the docked Files pane")
	}
}
