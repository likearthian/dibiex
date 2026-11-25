package app

import (
	"strings"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestTableObjectsOpenInspector(t *testing.T) {
	table := database.Table{Schema: "public", Name: "events", Kind: "Tables"}
	makeDefinition := func(name, kind, sql string) database.Definition {
		return database.Definition{Name: name, Kind: kind, SQL: sql, Properties: []database.Property{{Name: "Native marker", Value: name}}}
	}
	m := database.TableMetadata{Kind: "Tables", FetchedAt: time.Now(), DDL: "CREATE TABLE events (id INTEGER);", Columns: []database.Column{{Name: "id", Type: "integer", ClusteringOrdinal: 1}}, Clustering: []string{"id"}, Constraints: []database.Definition{makeDefinition("events_pkey", "p", "PRIMARY KEY (id)"), makeDefinition("id_positive", "c", "CHECK (id > 0)")}, Indexes: []database.Definition{makeDefinition("events_idx", "INDEX", "CREATE INDEX events_idx ON events(id)")}, Triggers: []database.Definition{makeDefinition("events_trigger", "TRIGGER", "CREATE TRIGGER events_trigger BEFORE UPDATE ON events EXECUTE FUNCTION updated()")}, Partitions: []database.Definition{makeDefinition("events_2026", "PARTITION", "FOR VALUES FROM (1) TO (10)")}, Policies: []database.Definition{makeDefinition("tenant_policy", "POLICY", "id > 0")}, Privileges: []database.Definition{makeDefinition("analyst_select", "GRANT", "")}, Rules: []database.Definition{makeDefinition("events_rule", "RULE", "CREATE RULE events_rule AS ON INSERT TO events DO NOTHING")}, Properties: []database.Property{{Name: "Table setting", Value: "visible"}}, Sections: map[string]database.MetadataSection{}}
	a := &App{connection: "Database", explorerWidth: 400, editorPercent: 60, hideSearch: true, hideResults: true, showFiles: true}
	a.applyCatalog([]database.Schema{{Name: "public", Loaded: true, Tables: []database.Table{table}}}, true)
	a.treeClosed["table:"+table.ID()] = false
	a.metadata = map[string]database.TableMetadata{table.ID(): m}
	a.tableSectionsOpen = map[string]bool{}
	for _, section := range []string{"columns", "keys", "indexes", "triggers", "partitions", "constraints", "policies", "privileges", "rules", "clustering"} {
		a.tableSectionsOpen[sectionKey(table, section)] = true
		m.Sections[section] = database.MetadataSection{State: "loaded"}
	}
	ui.RegisterFont(monoFont, "JetBrains Mono")
	v := ui.NewTester(a.view, 1472, 1200)
	v.SetDark(true)
	for _, section := range []string{"Properties", "DDL"} {
		if _, ok := v.Find("Toggle " + section + " for public.events"); ok {
			t.Fatalf("%s still present in sidebar", section)
		}
	}
	for _, section := range []string{"keys", "indexes", "triggers", "partitions", "constraints", "policies", "privileges", "rules", "clustering"} {
		item := tableDefinitionItems(m, section)[0]
		name := tableDefinitionName(item, 0)
		label := "Inspect " + section + " " + name
		before, _ := v.Find("Inspect clustering id")
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
		if !a.showInspector || a.inspectorDefinition == nil || a.inspectorDefinition.Section != section {
			t.Fatalf("%s did not open Inspector", section)
		}
		if _, ok := v.Find("Inspected table object " + name); !ok {
			t.Fatalf("Inspector title missing for %s", name)
		}
		after, _ := v.Find("Inspect clustering id")
		if before.Y != after.Y {
			t.Fatalf("%s expanded sidebar rows", section)
		}
		if a.propertyOpen[definitionTreeKey(table, section, 0, name)] {
			t.Fatalf("%s opened inline properties", section)
		}
		if err := v.Click(label); err != nil {
			t.Fatal(err)
		}
		if a.inspectorDefinition == nil {
			t.Fatal("second selection closed Inspector")
		}
		if !strings.HasSuffix(strings.Join(a.objectPath, "/"), section+"/"+name) {
			t.Fatalf("wrong object breadcrumb: %v", a.objectPath)
		}
	}
	a.showInspector = false
	a.inspectTable(table)
	if !a.showInspector {
		t.Fatal("table selection did not open Inspector")
	}
	v.Frame()
	if err := v.Click("Properties"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("Table setting: visible") {
		t.Fatal("table Properties missing from Inspector")
	}
	if err := v.Click("DDL"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("CREATE TABLE events") {
		t.Fatal("table DDL missing from Inspector")
	}
	if err := v.Click("Inspect indexes events_idx"); err != nil {
		t.Fatal(err)
	}
	m.Indexes[0].SQL = "CREATE UNIQUE INDEX events_idx ON events(id)"
	a.metadata[table.ID()] = m
	a.refreshTableObjectInspection(table, m)
	v.Frame()
	if !strings.Contains(a.inspectorDefinition.Object.SQL, "UNIQUE") {
		t.Fatal("refresh retained stale index details")
	}
	m.Indexes = nil
	a.metadata[table.ID()] = m
	a.refreshTableObjectInspection(table, m)
	v.Frame()
	if a.inspectorDefinition != nil {
		t.Fatal("removed index retained in Inspector")
	}
}
