package app

import (
	"context"
	"fmt"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestRowCountToastAcrossConnections(t *testing.T) {
	client, err := database.Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	a := &App{client: client, connection: "First", explorerWidth: 260, editorPercent: 60}
	a.addQuery("SELECT 42;")
	before := a.currentEditor()
	b := a.newConnectionSession()
	b.connection = "Second"
	table := database.Table{Schema: "public", Name: "events"}
	a.completeRowCount(client, table, "First", 123, nil)
	if len(a.tabs) != 1 || a.currentEditor() != before || a.querySQL() != "SELECT 42;" || len(b.tabs) != 0 || a.ensureWorkspace().active != b {
		t.Fatal("counting rows changed editor tabs or the active connection")
	}
	v := ui.NewTester(a.view, 1180, 780)
	if !v.HasText("public.events · 123 rows") || len(a.pendingToasts) != 0 {
		t.Fatal("row count toast did not show while another connection was selected")
	}
	a.completeRowCount(client, table, "First", 0, fmt.Errorf("permission denied"))
	v.Frame()
	if !v.HasText("Could not count rows · public.events") || !v.HasText("First · permission denied") {
		t.Fatal("row count errors must appear in a toast")
	}
	a.client = nil
	a.completeRowCount(client, table, "First", 1, nil)
	if len(a.pendingToasts) != 0 {
		t.Fatal("disconnected client produced a stale notification")
	}
}
