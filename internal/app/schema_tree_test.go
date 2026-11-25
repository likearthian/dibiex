package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestNestedSchemaTree(t *testing.T) {
	table := database.Table{Project: "analytics", Schema: "public", Name: "events", Kind: "Partitioned tables"}
	a := &App{connection: "Warehouse", explorerWidth: 340, editorPercent: 55, query: "SELECT * FROM events;", connections: []savedConnection{{Name: "Cloud warehouse", Config: database.Config{Engine: database.BigQuery}}, {Name: "Application", Config: database.Config{Engine: database.PostgreSQL}}}}
	a.applyCatalog([]database.Schema{{Project: "analytics", Name: "public", Loaded: true, Tables: []database.Table{table, {Project: "analytics", Schema: "public", Name: "event_names", Kind: "Views"}, {Project: "analytics", Schema: "public", Name: "daily_events", Kind: "Materialized views"}}}}, true)
	a.treeClosed["table:"+table.ID()] = false
	a.metadata = map[string]database.TableMetadata{table.ID(): {Kind: "Partitioned tables", FetchedAt: time.Now(), Columns: []database.Column{
		{Name: "id", Type: "INT64", PrimaryKey: true, ClusteringOrdinal: 1},
		{Name: "account_id", Type: "INT64", ForeignKey: true},
		{Name: "event_ts", Type: "TIMESTAMP", Partitioning: true},
		{Name: "payload", Type: "RECORD", Fields: []database.Column{{Name: "user", Type: "RECORD", Fields: []database.Column{{Name: "email", Type: "STRING"}}}}},
		{Name: "items", Type: "ARRAY<RECORD>", Fields: []database.Column{{Name: "sku", Type: "STRING"}}},
	}}}
	v := ui.NewTester(a.view, 1180, 780)
	if _, ok := v.Find("Data type INT64 for id"); ok {
		t.Fatal("columns must start inside a collapsed Columns branch")
	}
	for _, section := range []string{"Columns", "Indexes", "Partitions", "Constraints", "Triggers", "Policies", "Privileges", "Rules"} {
		if _, ok := v.Find("Toggle " + section + " for public.events"); !ok {
			t.Fatalf("missing table branch %s", section)
		}
	}
	for _, section := range []string{"Properties", "DDL"} {
		if _, ok := v.Find("Toggle " + section + " for public.events"); ok {
			t.Fatalf("%s should only be in Inspector", section)
		}
	}
	if err := v.Click("Toggle Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Partitioned tables", "Views", "Materialized views"} {
		if !v.HasText(label) {
			t.Fatalf("missing schema UI %s", label)
		}
	}
	for _, label := range []string{"BQ", "PG", "SQLITE"} {
		if v.HasText(label) {
			t.Fatalf("connection engine pill still visible: %s", label)
		}
	}
	for _, label := range []string{"BigQuery", "PostgreSQL", "SQLite"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("database brand icon missing: %s", label)
		}
	}
	for _, label := range []string{"Data type INT64 for id", "Data type TIMESTAMP for event_ts", "Data type ARRAY<RECORD> for items"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing data type icon: %s", label)
		}
	}
	for _, name := range []string{"id", "event_ts", "items"} {
		if _, ok := v.Find("Column details for " + name); !ok {
			t.Fatalf("missing inline column details: %s", name)
		}
	}
	if v.HasText("email") {
		t.Fatal("nested fields should start collapsed")
	}
	if err := v.Click("Toggle field payload"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Toggle field payload.user"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("email") {
		t.Fatal("nested record did not expand")
	}
	if err := v.Click("Toggle field items"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("sku") {
		t.Fatal("repeated record did not expand")
	}
	if err := v.Click("Toggle Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	if v.HasText("sku") {
		t.Fatal("Columns branch did not collapse")
	}
	if _, ok := v.Find("Toggle Indexes for public.events"); !ok {
		t.Fatal("collapsing Columns hid sibling branches")
	}
	if err := v.Click("Toggle Columns for public.events"); err != nil {
		t.Fatal(err)
	}
	if !v.HasText("sku") {
		t.Fatal("nested expansion state lost when Columns collapsed")
	}
	a.filter = "email"
	v.Frame()
	if !v.HasText("events") || !v.HasText("email") || v.HasText("event_names") {
		t.Fatal("nested field search must reveal only matching loaded objects")
	}
	a.filter = ""
	v.Frame()
	for _, width := range []float32{340, 250, 190} {
		a.explorerWidth = width
		v.Frame()
		for _, label := range []string{"Select Warehouse", "Select public", "Select Partitioned tables", "Inspect table public.events", "Select Columns for public.events", "Select Indexes for public.events", "Column payload", "Column id", "Column payload.user.email"} {
			r, ok := v.Find(label)
			if !ok || r.X < 8.5 || r.X > 9.5 || r.W < width-3 || r.W > width-1 {
				t.Fatalf("%s does not fill sidebar inside the outline at %.0f: %+v", label, width, r)
			}
		}
		button, ok := v.Find("Actions for public.events")
		if !ok || button.X+button.W > width+8 || button.X+button.W < width {
			t.Fatalf("object action is not pinned to sidebar edge: %+v at %.0f", button, width)
		}
		label, _ := v.Find("Partitioned tables")
		count, ok := v.Find("Object count for Partitioned tables")
		if !ok || count.X < label.X+label.W || count.X-label.X-label.W > 8 {
			t.Fatalf("count is detached from label: label=%+v count=%+v", label, count)
		}
		icon, _ := v.Find("Data type TIMESTAMP for event_ts")
		name, _ := v.Find("event_ts")
		if icon.X+icon.W > name.X || name.X-icon.X-icon.W > 8 {
			t.Fatalf("type icon must precede column name: icon=%+v name=%+v", icon, name)
		}
	}
	a.explorerWidth = 340
	v.Frame()
	if dir := os.Getenv("DBCRAFT_SCREENSHOTS"); dir != "" {
		for _, dark := range []bool{false, true} {
			v.SetDark(dark)
			name := "schema-tree-light.png"
			if dark {
				name = "schema-tree-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, name))
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
}
