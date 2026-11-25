package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestAdditionalBigQueryProjectsPersistWithoutChangingCredentials(t *testing.T) {
	original := database.Config{Engine: database.BigQuery, Project: "billing", DatasetProject: "primary", AdditionalProjects: []string{"existing"}, MaxBytesBilled: 1234}
	cfg, err := addBigQueryProjectConfig(original, "  extra-project  ")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(original.AdditionalProjects, []string{"existing"}) || !slices.Equal(cfg.BigQueryProjects(), []string{"primary", "existing", "extra-project"}) {
		t.Fatal("adding a project mutated the original settings")
	}
	if credentialKey("Analytics", original) != credentialKey("Analytics", cfg) || !sameConnection(original, cfg) {
		t.Fatal("browsing projects changed credential or session identity")
	}
	for _, project := range []string{"primary", "existing", "extra-project", "", "https://example.com"} {
		if _, err := addBigQueryProjectConfig(cfg, project); err == nil {
			t.Fatalf("accepted invalid or duplicate project %q", project)
		}
	}
	path := filepath.Join(t.TempDir(), "connections.json")
	if err := writeConnections(path, []savedConnection{{Name: "Analytics", Config: cfg}}); err != nil {
		t.Fatal(err)
	}
	saved, err := readConnections(path)
	if err != nil || len(saved) != 1 || !slices.Equal(saved[0].Config.BigQueryProjects(), cfg.BigQueryProjects()) {
		t.Fatalf("additional projects were not persisted: %v %v", saved, err)
	}
	if saved[0].Config.Project != original.Project || saved[0].Config.MaxBytesBilled != original.MaxBytesBilled {
		t.Fatal("adding a project changed billing settings")
	}
}

func TestBigQueryProjectNodesRemainIndependent(t *testing.T) {
	first := database.Table{Project: "first-project", Schema: "shared", Name: "events", Kind: "Tables"}
	second := first
	second.Project = "second-project"
	a := &App{connection: "Analytics", explorerWidth: 400, hideSearch: true, explorerCatalog: database.ExplorerCatalog{Engine: database.BigQuery}}
	a.applyCatalog([]database.Schema{
		{Project: first.Project, Name: "shared", Loaded: true, Tables: []database.Table{first}},
		{Project: second.Project, Name: "shared", Loaded: true, Tables: []database.Table{second}},
	}, true)
	v := ui.NewTester(func(c *ui.Context) { a.sidebar(c, paletteOf(c)) }, 400, 900)
	if err := v.Click("Toggle first-project"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("events") {
		t.Fatal("collapsing one project hid the other's same-named dataset")
	}
	a.selectTreePath("schema:"+a.schemas[1].ID(), "shared")
	if !slices.Equal(a.objectPath, []string{"Database", "Analytics", "second-project", "shared"}) {
		t.Fatalf("dataset breadcrumb used another project: %v", a.objectPath)
	}
	if got := strings.Join(a.tableObjectPath(second), "/"); got != "Database/Analytics/second-project/shared/events" {
		t.Fatalf("table breadcrumb used another project: %s", got)
	}
}

func TestRunningQueryRefreshPreservesNewBigQueryProject(t *testing.T) {
	current := []database.Schema{{Project: "primary", Name: "old"}, {Project: "just-added", Name: "shared"}}
	refreshed := []database.Schema{{Project: "primary", Name: "new"}}
	got := mergeBigQueryCatalog(current, refreshed, []string{"primary"})
	if len(got) != 2 || got[0].Name != "new" || got[1].Project != "just-added" {
		t.Fatalf("an older query's catalog refresh lost a newly added project: %v", got)
	}
}

func TestRemoveBigQueryProjectPersistsAndCanBeReadded(t *testing.T) {
	original := database.Config{Engine: database.BigQuery, Project: "billing", DatasetProject: "primary", Dataset: "default_dataset", AdditionalProjects: []string{"extra"}, MaxBytesBilled: 1234}
	for _, project := range []string{"primary", "extra"} {
		cfg, err := removeBigQueryProjectConfig(original, project)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(cfg.BigQueryProjects(), project) || !slices.Contains(original.BigQueryProjects(), project) {
			t.Fatal("removal mutated the original or left the project visible")
		}
		if cfg.Project != original.Project || cfg.DatasetProject != original.DatasetProject || cfg.Dataset != original.Dataset || cfg.MaxBytesBilled != original.MaxBytesBilled || credentialKey("Analytics", cfg) != credentialKey("Analytics", original) || !sameConnection(cfg, original) {
			t.Fatal("removing a tree project changed query defaults or credentials")
		}
		path := filepath.Join(t.TempDir(), "connections.json")
		if err := writeConnections(path, []savedConnection{{Name: "Analytics", Config: cfg}}); err != nil {
			t.Fatal(err)
		}
		saved, err := readConnections(path)
		if err != nil || len(saved) != 1 || slices.Contains(saved[0].Config.BigQueryProjects(), project) {
			t.Fatalf("removal did not survive reload: %v %v", saved, err)
		}
		restored, err := addBigQueryProjectConfig(saved[0].Config, project)
		if err != nil || !slices.Contains(restored.BigQueryProjects(), project) || len(restored.BigQueryProjects()) != 2 {
			t.Fatalf("removed project could not be restored: %v %v", restored, err)
		}
	}
}

func TestRemoveBigQueryProjectPrunesOnlyItsTreeState(t *testing.T) {
	removed := database.Table{Project: "primary", Schema: "shared", Name: "events"}
	retained := removed
	retained.Project = "anotherprimary"
	a := &App{connection: "Analytics", explorerCatalog: database.ExplorerCatalog{Engine: database.BigQuery},
		schemas:        []database.Schema{{Project: removed.Project}, {Project: retained.Project}},
		metadata:       map[string]database.TableMetadata{removed.ID(): {}, retained.ID(): {}},
		treeClosed:     map[string]bool{"project:primary": true, "schema:primary\x00shared": true, "schema:anotherprimary\x00shared": true},
		inspectorTable: removed, inspectorColumn: &columnInspection{Table: removed},
		activeTable: removed.ID(), selectedTree: "table:" + removed.ID(),
		objectPath: []string{"Database", "Analytics", "primary", "shared", "events"},
		tabs:       []*queryTab{{querySchemas: []database.Schema{{Project: removed.Project}, {Project: retained.Project}}}},
	}
	a.pruneBigQueryProject("primary")
	if len(a.schemas) != 1 || a.schemas[0].Project != retained.Project || len(a.tabs[0].querySchemas) != 1 || len(a.metadata) != 1 || len(a.treeClosed) != 1 {
		t.Fatal("removal failed to isolate project state")
	}
	if _, ok := a.metadata[retained.ID()]; !ok || !a.treeClosed["schema:anotherprimary\x00shared"] {
		t.Fatal("removal cleared another project with a similar name")
	}
	if a.activeTable != "" || a.inspectorTable.Project != "" || a.inspectorColumn != nil || !slices.Equal(a.objectPath, []string{"Database", "Analytics"}) {
		t.Fatal("removal left a stale Inspector selection or breadcrumb")
	}
	// A query started before removal must not put its old catalog back in the tree.
	old := []database.Schema{{Project: removed.Project}, {Project: retained.Project}}
	visible := visibleBigQueryCatalog(mergeBigQueryCatalog(a.schemas, old, []string{removed.Project, retained.Project}), []string{retained.Project})
	if len(visible) != 1 || visible[0].Project != retained.Project {
		t.Fatal("an older query resurrected a removed project")
	}
}
