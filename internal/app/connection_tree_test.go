package app

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestConnectionSpinnerBelongsToConnectingConnection(t *testing.T) {
	a := &App{explorerWidth: 280, editorPercent: 60, busy: true,
		connections: []savedConnection{{Name: "First", Config: database.Config{Engine: database.PostgreSQL}}, {Name: "Second", Config: database.Config{Engine: database.SQLite}}}}
	pending := a.newConnectionSession()
	pending.lastConnection = &savedConnection{Name: "Second", Config: database.Config{Engine: database.SQLite}}
	pending.connecting = true
	a.activateConnection()
	v := ui.NewTester(a.view, 1180, 780)
	if _, ok := v.Find("Connecting Second"); !ok {
		t.Fatal("connecting connection is missing its spinner")
	}
	if _, ok := v.Find("Connecting First"); ok {
		t.Fatal("unrelated busy work showed a connection spinner")
	}
	pending.connecting = false
	v.Frame()
	if _, ok := v.Find("Connecting Second"); ok {
		t.Fatal("connection spinner remained after connection attempt finished")
	}
	pending.connecting = true
	pending.disconnectConnection()
	if pending.connecting {
		t.Fatal("disconnect did not clear the connecting state")
	}
}

func TestConnectionTreeHasOneActiveRoot(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Demo workspace", explorerWidth: 280, editorPercent: 55, query: "SELECT * FROM projects;", connections: []savedConnection{{Name: "Development", Config: client.Settings()}, {Name: "Warehouse", Config: database.Config{Engine: database.BigQuery, Project: "analytics"}}}}
	schemas, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.applyCatalog(schemas, true)
	v := ui.NewTester(a.view, 1180, 780)
	count := 0
	for _, text := range v.Texts() {
		if text == "Select Development" {
			count++
		}
	}
	// Count root rows; connection cues also appear in tabs and the toolbar.
	_, duplicateSavedRoot := v.Find("Connect Development")
	if count != 1 || duplicateSavedRoot || v.HasText("Demo workspace") || !v.HasText("projects") {
		t.Fatalf("duplicate connection roots: %v", v.Texts())
	}
	if err = v.Click("Toggle Development"); err != nil {
		t.Fatal(err)
	}
	if v.HasText("projects") || !v.HasText("Warehouse") {
		t.Fatal("collapsing the active connection hid other connections")
	}
	if err = v.Click("Toggle Development"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Actions for Warehouse"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Modify connection…"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection || a.connectionForm.name != "Warehouse" {
		t.Fatal("inactive connection did not open its connection form")
	}
	if _, ok := v.Find("Connection name"); !ok {
		t.Fatal("connection terminology missing")
	}
	for _, text := range v.Texts() {
		if strings.Contains(strings.ToLower(text), "profile") {
			t.Fatalf("old terminology visible: %s", text)
		}
	}
	if err = v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "connections-unified.png"))
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

func TestCatalogPickerTogglesSchemaVisibility(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Local", explorerWidth: 280, editorPercent: 60}
	schemas, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.applyCatalog(schemas, true)
	v := ui.NewTester(a.view, 1180, 780)
	click := func(label string) {
		t.Helper()
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := v.Find("Choose databases for Local"); ok {
		t.Fatal("SQLite connection row has a visibility button")
	}
	click("Choose databases for Databases · 1 of 1")
	click("All databases")
	v.ClickAt(1000, 500)
	if _, ok := v.Find("Select main"); ok || v.HasText("projects") {
		t.Fatal("hidden schema is still in the explorer")
	}
	if len(a.schemas) == 0 {
		t.Fatal("hiding the schema removed it from the catalog")
	}
	click("Choose databases for Databases · 0 of 1")
	click("Show database main")
	v.Key(0, ui.KeyEnter)
	if !v.HasText("projects") {
		t.Fatal("re-enabling the schema did not restore its tables")
	}
}

func TestCatalogPickerShowsDatasetsUnderExpandedProject(t *testing.T) {
	schema := database.Schema{Project: "analytics", Name: "warehouse"}
	a := &App{
		schemas: []database.Schema{schema},
		catalogPicker: catalogPicker{
			projects:       []string{"other"},
			expanded:       make(map[string]bool),
			projectChoices: catalogChoices(nil),
			schemaChoices:  catalogChoices(nil),
		},
	}
	v := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() { a.catalogProjectChoices(c, paletteOf(c), "") })
	}, 400, 400)
	if _, ok := v.Find("Show dataset warehouse"); ok {
		t.Fatal("datasets were visible before expanding their project")
	}
	if err := v.Click("Toggle datasets in analytics"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("All datasets"); err != nil {
		t.Fatal(err)
	}
	if *a.catalogPicker.schemaChoices[schema.ID()] || !*a.catalogPicker.projectChoices["analytics"] {
		t.Fatal("dataset bulk selection changed the owning project choice")
	}
}

func TestDeletingConnectionClosesSession(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	session, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{client: client, connection: "Development", sessionContext: session, sessionCancel: cancel, connections: []savedConnection{{Name: "Development", Config: client.Settings()}, {Name: "Other", Config: database.Config{Engine: database.PostgreSQL}}}, schemas: []database.Schema{{Name: "main", Loaded: true}}, result: database.Result{Columns: []string{"old"}, Rows: [][]string{{"value"}}}}
	queryCtx, queryCancel := context.WithCancel(session)
	a.cancel = queryCancel
	done := a.applyConnectionRemoval(a.connections[1:], client, true)
	if a.hasConnection() || a.client != nil || len(a.schemas) != 0 || len(a.result.Rows) != 0 || a.engineLabel() != "No connection" {
		t.Fatal("active session state remained after removal")
	}
	if session.Err() == nil || queryCtx.Err() == nil {
		t.Fatal("connection work was not cancelled")
	}
	if len(a.connections) != 1 || a.connections[0].Name != "Other" {
		t.Fatal("unrelated connection was removed")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("database session did not close")
	}
	if _, err = client.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("deleted connection is still usable")
	}
	// A delayed deletion must not disconnect a newer session.
	newClient, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	a.client, a.connection = newClient, "New connection"
	<-a.applyConnectionRemoval(a.connections, client, true)
	if a.client != newClient {
		t.Fatal("late deletion closed a different session")
	}
}

func TestDeleteUnsavedConnectionMenuLeavesEmptyWorkspace(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "Temporary", explorerWidth: 250, editorPercent: 55}
	v := ui.NewTester(a.view, 800, 560)
	if err = v.Click("Actions for Temporary"); err != nil {
		t.Fatal(err)
	}
	if err = v.Click("Delete connection"); err != nil {
		t.Fatal(err)
	}
	_, rootStillVisible := v.Find("Toggle Temporary")
	if a.hasConnection() || !v.HasText("No connections") || rootStillVisible || v.HasText("Demo workspace") {
		t.Fatal("deleted connection reappeared in the tree")
	}
	if err = v.Click("Add connection"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection {
		t.Fatal("could not add a connection after deleting the last one")
	}
}

func TestConnectionRenameUpdatesExistingEntry(t *testing.T) {
	existing := []savedConnection{{Name: "Old", Config: database.Config{Engine: database.SQLite, Path: "app.db"}}, {Name: "Other", Config: database.Config{Engine: database.PostgreSQL}}}
	next := savedConnection{Name: "New", Config: existing[0].Config}
	updated, err := upsertConnection(existing, "Old", next)
	if err != nil || len(updated) != 2 || updated[0].Name != "New" || existing[0].Name != "Old" {
		t.Fatalf("rename created a duplicate or mutated the input: %+v %v", updated, err)
	}
	if _, err = upsertConnection(existing, "Old", savedConnection{Name: "Other"}); err == nil {
		t.Fatal("rename overwrote another connection")
	}
	// The JSON format stays compatible with the existing connections file.
	path := filepath.Join(t.TempDir(), "connections.json")
	if err = writeConnections(path, updated); err != nil {
		t.Fatal(err)
	}
	read, err := readConnections(path)
	if err != nil || len(read) != 2 || read[0].Name != "New" {
		t.Fatalf("saved connection reload: %+v %v", read, err)
	}
}

func TestExplorerDoubleClickOpensTableView(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	schemas, err := client.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{client: client, connection: "Local", explorerWidth: 280, hideResults: true}
	a.applyCatalog(schemas, true)
	v := ui.NewTester(a.view, 1180, 780)
	label := "Inspect table projects"
	if err := v.Click(label); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 0 {
		t.Fatal("single click opened a tab")
	}
	if err := v.Click(label); err != nil {
		t.Fatal(err)
	}
	tab := a.currentEditor()
	if tab == nil || tab.tableView == nil || tab.tableView.Name != "projects" {
		t.Fatal("double click did not open the table view")
	}
	a.openTableView(*tab.tableView)
	if len(a.tabs) != 1 || a.currentEditor() != tab {
		t.Fatal("opening the same table did not reuse its tab")
	}
}
