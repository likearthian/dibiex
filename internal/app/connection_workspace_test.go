package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func workspaceClient(t *testing.T, name string) (*database.Client, []database.Schema) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".sqlite")
	if err := database.CreateSQLite(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	client, err := database.Connect(context.Background(), database.Config{Engine: database.SQLite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if _, err = client.ExecuteResult(context.Background(), "CREATE TABLE "+name+" (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	schemas, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return client, schemas
}

func TestMultipleConnectionWorkspaces(t *testing.T) {
	first, firstSchemas := workspaceClient(t, "first_table")
	second, secondSchemas := workspaceClient(t, "second_table")
	a := &App{explorerWidth: 300, editorPercent: 60, hideResults: true, hideSearch: true}
	a.installConnection(first, nil, firstSchemas, "First", "")
	a.addQuery("SELECT 'first' AS source")
	a.result = database.Result{Columns: []string{"source"}, Rows: [][]string{{"first"}}}
	a.objectPath = []string{"First", "first_table"}
	a.busy = true
	queryContext, cancel := context.WithCancel(a.connectionContext())
	a.cancel = cancel
	t.Cleanup(cancel)
	b := a.installConnection(second, nil, secondSchemas, "Second", "")
	t.Cleanup(func() { a.cancelConnections() })
	b.addQuery("SELECT 'second' AS source")
	b.result = database.Result{Columns: []string{"source"}, Rows: [][]string{{"second"}}}
	if b == a || a.connectionWorkspace.active != b || len(a.connectionSessions()) != 2 {
		t.Fatal("opening another connection did not create a separate workspace")
	}
	if queryContext.Err() != nil || !a.busy || a.client != first || a.result.Rows[0][0] != "first" {
		t.Fatal("opening a connection changed the first session or cancelled its query")
	}
	v := ui.NewTester(a.view, 1180, 780)
	for _, label := range []string{"Select First", "Select Second"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing active connection root %s", label)
		}
	}
	if err := v.Click("Select First"); err != nil {
		t.Fatal(err)
	}
	if a.connectionWorkspace.active != a || a.querySQL() != "SELECT 'first' AS source" || queryContext.Err() != nil {
		t.Fatal("switching connection lost its editor or cancelled its query")
	}
	if err := v.Click("Select Second"); err != nil {
		t.Fatal(err)
	}
	if a.connectionWorkspace.active != b || b.querySQL() != "SELECT 'second' AS source" || b.result.Rows[0][0] != "second" {
		t.Fatal("switching connection mixed editor/results between sessions")
	}
	a.busy = false
	if _, ok := v.Find("Select Databases"); !ok {
		t.Fatal("new connection did not show its Databases node")
	}
	for _, label := range []string{"Toggle Databases", "Toggle main", "Toggle Tables"} {
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Click("Inspect table first_table"); err != nil {
		t.Fatal(err)
	}
	if a.connectionWorkspace.active != a || a.inspectorTable.Name != "first_table" || !a.showInspector || b.showInspector {
		t.Fatal("selecting an object in another connection did not select its owning Inspector")
	}
	// Queries remain bound to their owning database after UI switches.
	for _, session := range []*App{a, b} {
		result, err := session.client.Query(session.connectionContext(), session.querySQL())
		if err != nil || result.Rows[0][0] != session.result.Rows[0][0] {
			t.Fatalf("connection query routing: %+v %v", result, err)
		}
	}
	a.setSavedConnections([]savedConnection{{Name: "First", Config: first.Settings()}, {Name: "Second", Config: second.Settings()}})
	a.setConnectionStoreBusy(true)
	if !b.connectionStoreBusy || len(b.connections) != 2 {
		t.Fatal("connection settings were not shared across workspaces")
	}
	a.setConnectionStoreBusy(false)
	select {
	case err := <-a.applyConnectionRemoval(a.connections[1:], first, true):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not close the selected connection")
	}
	if queryContext.Err() == nil || b.connectionContext().Err() != nil || b.client != second || len(b.connections) != 1 {
		t.Fatal("disconnect affected the wrong session")
	}
	if _, err := b.client.Query(b.connectionContext(), "SELECT * FROM second_table"); err != nil {
		t.Fatalf("surviving connection is unusable: %v", err)
	}
	a.cancelConnections()
	if !a.closed || !b.closed || b.connectionContext().Err() == nil {
		t.Fatal("shutdown did not cancel every connection")
	}
}

func TestAddConnectionWhileAnotherQueryRuns(t *testing.T) {
	client, schemas := workspaceClient(t, "running")
	a := &App{explorerWidth: 280, editorPercent: 60, hideSearch: true}
	a.installConnection(client, nil, schemas, "Running", "")
	t.Cleanup(func() { a.cancelConnections() })
	ctx, cancel := context.WithCancel(a.connectionContext())
	a.busy, a.cancel = true, cancel
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Add connection"); err != nil {
		t.Fatal(err)
	}
	blank := a.connectionWorkspace.active
	if blank == a || !blank.showConnection || blank.busy || !a.busy || ctx.Err() != nil {
		t.Fatal("adding another connection interrupted the running connection")
	}
	// Connecting to an already active named configuration selects its session.
	blank.connectionForm.name = "Running"
	blank.connect(client.Settings())
	if a.connectionWorkspace.active != a || blank.showConnection || len(a.connectionSessions()) != 2 || ctx.Err() != nil {
		t.Fatal("existing connection was duplicated or its work was cancelled")
	}
	blank.showConnection = true
	blank.connectionForm.name = "Running"
	changed := client.Settings()
	changed.Path += ".different"
	blank.connect(changed)
	if blank.connectionForm.errorText == "" || !blank.showConnection {
		t.Fatal("an active connection name was reused for different settings")
	}
}
