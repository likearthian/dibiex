package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestQueryLibraryPersistenceAndFiles(t *testing.T) {
	records := []savedQuery{{ID: "first", Name: "Accounts.sql", SQL: "SELECT 'é';\nSELECT NULL;", Connection: "Development"}, {ID: "second", Name: "Summary.sql", SQL: "SELECT 2;", Connection: "Analytics"}}
	path := filepath.Join(t.TempDir(), "queries.json")
	if err := writeQueryLibrary(path, records); err != nil {
		t.Fatal(err)
	}
	loaded, err := readQueryLibrary(path)
	if err != nil || len(loaded) != 2 || loaded[0] != records[0] {
		t.Fatalf("library restart round trip: %v", err)
	}
	a := &App{explorerWidth: 260, editorPercent: 60, showFiles: true, hideResults: true, hideSearch: true}
	w := a.ensureWorkspace()
	w.queries = loaded
	a.ensureQueryOwners()
	if len(a.workspaceTabs()) != 0 {
		t.Fatal("restoring the library opened query tabs")
	}
	v := ui.NewTester(a.view, 1472, 884)
	if err := v.Click("Open saved query Accounts.sql · Development"); err != nil {
		t.Fatal(err)
	}
	owner := w.active
	if owner.workspaceName() != "Development" || owner.currentEditor().savedID != "first" || owner.querySQL() != "SELECT 'é';" {
		t.Fatal("saved query opened under the wrong connection or with the wrong SQL")
	}
	owner.openSavedQuery(records[0])
	if len(owner.tabs) != 1 {
		t.Fatal("opening an existing saved tab duplicated it")
	}
	owner.closeQueryTab()
	v.Frame()
	if len(a.workspaceTabs()) != 0 {
		t.Fatal("closing the last saved tab recreated a console")
	}
	if _, ok := v.Find("Open saved query Accounts.sql · Development"); !ok {
		t.Fatal("closing a tab removed its saved query")
	}
	if err := v.Click("Actions for saved query Accounts.sql"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Delete"); !ok {
		t.Fatal("Files context menu missing Remove")
	}
	menu, ok := v.Find("Saved query menu")
	if !ok || menu.W > 170 {
		t.Fatalf("context menu stretched to the Files panel: %+v", menu)
	}
	if err := v.Click("Rename"); err != nil {
		t.Fatal(err)
	}
	if !w.active.showQueryName || w.active.queryName.savedID != "first" {
		t.Fatal("Files rename did not open its query-name dialog")
	}
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if len(a.workspaceTabs()) != 0 {
		t.Fatal("opening query actions unexpectedly opened a query tab")
	}
	if err := v.RightClick("Open saved query Accounts.sql · Development"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Rename", "Delete"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("right-click menu missing %s", label)
		}
	}
	if _, ok := v.Find("Rename saved query…"); ok {
		t.Fatal("long menu label remains")
	}
	if err := v.Click("Rename"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	loaded[0].Name = "Renamed.sql"
	if err := writeQueryLibrary(path, loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = readQueryLibrary(path)
	if err != nil || loaded[0].Name != "Renamed.sql" || loaded[0].SQL != records[0].SQL {
		t.Fatal("rename changed query contents")
	}
	if err := writeQueryLibrary(path, loaded[1:]); err != nil {
		t.Fatal(err)
	}
	loaded, err = readQueryLibrary(path)
	if err != nil || len(loaded) != 1 || loaded[0].ID != "second" {
		t.Fatal("removal changed another connection's query")
	}
}

func TestTabsCloseOnHoverAndStayEmpty(t *testing.T) {
	a := &App{connection: "Development", explorerWidth: 280, editorPercent: 60, hideSearch: true, hideResults: true}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{{Schema: "public", Name: "events", Kind: "Tables"}}}}, true)
	v := ui.NewTester(a.view, 1180, 780)
	if len(a.tabs) != 0 || !v.HasText("No query tabs") {
		t.Fatal("empty workspace created a tab")
	}
	if err := v.Click("Inspect table public.events"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 0 {
		t.Fatal("selecting a table created a query tab")
	}
	if err := v.Click("New query tab"); err != nil {
		t.Fatal(err)
	}
	v.Move(4, 4)
	if _, ok := v.Find("Close tab console.sql"); ok {
		t.Fatal("tab close button visible without hover")
	}
	if _, ok := v.Find("Close query tab"); ok {
		t.Fatal("old toolbar close button is still present")
	}
	r, _ := v.Find("console.sql")
	v.Move(r.X+r.W/2, r.Y+r.H/2)
	if err := v.Click("Close tab console.sql"); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if len(a.tabs) != 0 || !v.HasText("No query tabs") {
		t.Fatal("last tab was recreated")
	}
	if err := v.Click("Select public"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 0 {
		t.Fatal("selecting a schema created a tab")
	}
	if err := v.Click("Main menu"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Open SQL file…", "New query tab", "Save query", "Files", "Inspector"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("main menu missing %s", label)
		}
	}
}

func TestReadSQLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unicode.sql")
	for _, tc := range []struct {
		data  string
		valid bool
	}{{"\ufeffSELECT 'é';", true}, {"SELECT \x00", false}, {string([]byte{0xff, 0xfe}), false}, {strings.Repeat("x", maxSQLFileBytes+1), false}} {
		if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		text, err := readSQLFile(path)
		if tc.valid && (err != nil || text != "SELECT 'é';") {
			t.Fatalf("valid SQL file: %q %v", text, err)
		}
		if !tc.valid && err == nil {
			t.Fatal("invalid SQL file accepted")
		}
	}
}

func TestCloseInactiveTabPreservesSelection(t *testing.T) {
	a := &App{connection: "First"}
	a.addQuery("SELECT 1")
	b := a.newConnectionSession()
	b.connection = "Second"
	b.addQuery("SELECT 2")
	b.addQuery("SELECT 3")
	a.activateConnection()
	b.closeTab(b.tabs[0])
	if a.ensureWorkspace().active != a || len(b.tabs) != 1 || b.querySQL() != "SELECT 3" {
		t.Fatal("closing an inactive tab changed the selected connection or editor")
	}
	b.closeTab(b.tabs[0])
	a.closeQueryTab()
	if len(a.workspaceTabs()) != 0 || a.currentEditor() != nil {
		t.Fatal("last query could not be closed")
	}
}
