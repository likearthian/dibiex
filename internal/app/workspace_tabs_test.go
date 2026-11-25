package app

import (
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestCombinedWorkspaceTabsAndFiles(t *testing.T) {
	first, firstSchemas := workspaceClient(t, "first")
	second, secondSchemas := workspaceClient(t, "second")
	a := &App{explorerWidth: 260, editorPercent: 60, hideResults: true, showFiles: true, hideSearch: true}
	a.installConnection(first, nil, firstSchemas, "Development", "")
	a.addQuery("SELECT 'first' AS source")
	a.result = database.Result{Columns: []string{"source"}, Rows: [][]string{{"first"}}}
	b := a.installConnection(second, nil, secondSchemas, "Analytics", "")
	b.addQuery("SELECT 'second' AS source")
	b.result = database.Result{Columns: []string{"source"}, Rows: [][]string{{"second"}}}
	defer a.cancelConnections()
	v := ui.NewTester(a.view, 1472, 884)
	for _, label := range []string{"console.sql · Development", "console.sql · Analytics", "Console folder Development", "Console folder Analytics", "Open console console.sql · Development", "Open console console.sql · Analytics"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("combined workspace missing %s", label)
		}
	}
	for _, step := range []struct {
		label  string
		owner  *App
		source string
	}{
		{"console.sql · Development", a, "first"},
		{"console.sql · Analytics", b, "second"},
		{"Open console console.sql · Development", a, "first"},
		{"Open console console.sql · Analytics", b, "second"},
	} {
		if err := v.Click(step.label); err != nil {
			t.Fatal(err)
		}
		if a.connectionWorkspace.active != step.owner || !step.owner.showFiles || step.owner.result.Rows[0][0] != step.source {
			t.Fatalf("%s selected the wrong connection or hid Files", step.label)
		}
		result, err := step.owner.client.Query(step.owner.connectionContext(), step.owner.querySQL())
		if err != nil || result.Rows[0][0] != step.source {
			t.Fatalf("tab routed SQL to the wrong connection: %v", err)
		}
	}
	if err := v.Click("Toggle consoles for Development"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Open console console.sql · Development"); ok {
		t.Fatal("folder did not collapse")
	}
	if _, ok := v.Find("Open console console.sql · Analytics"); !ok {
		t.Fatal("collapsing one connection hid another's files")
	}
	if _, ok := v.Find("console.sql · Development"); !ok {
		t.Fatal("collapsing a folder hid its query tab")
	}
	if err := v.Click("Console folder Development"); err != nil {
		t.Fatal(err)
	}
	if !a.filesGroupClosed {
		t.Fatal("folder body expanded the tree")
	}
	tabRect, _ := v.Find("console.sql · Analytics")
	v.Move(tabRect.X+tabRect.W/2, tabRect.Y+tabRect.H/2)
	if err := v.Click("Close tab console.sql · Analytics"); err != nil {
		t.Fatal(err)
	}
	if len(b.tabs) != 0 || len(a.tabs) != 1 || a.connectionWorkspace.active != a || len(a.workspaceTabs()) != 1 {
		t.Fatal("closing the last console in one connection recreated it or closed another connection's tab")
	}
	if a.client != first || b.client != second {
		t.Fatal("closing a tab closed its connection")
	}
	if _, err := second.Query(b.connectionContext(), "SELECT * FROM second"); err != nil {
		t.Fatal(err)
	}
}

func TestNewConnectionDoesNotRetargetDisconnectedDrafts(t *testing.T) {
	first, schemas := workspaceClient(t, "original")
	second, otherSchemas := workspaceClient(t, "replacement")
	a := &App{}
	a.installConnection(first, nil, schemas, "Original", "")
	a.addQuery("SELECT * FROM original")
	<-a.disconnectConnection()
	b := a.installConnection(second, nil, otherSchemas, "Other", "")
	defer a.cancelConnections()
	if b == a || a.client != nil || a.workspaceName() != "Original" || a.querySQL() != "SELECT * FROM original" {
		t.Fatal("opening another connection reassigned disconnected drafts")
	}
	if b.workspaceName() != "Other" || b.client != second {
		t.Fatal("new connection did not get its own workspace")
	}
}
