package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestExtractParameters(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		engine   database.Engine
		wantDefs []string
		wantOccs int
	}{
		{
			name:     "named parameters",
			sql:      "SELECT * FROM sales WHERE circle = :circle AND region = :region;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":circle", ":region"},
			wantOccs: 2,
		},
		{
			name:     "repeated named parameter",
			sql:      "SELECT * FROM sales WHERE start_date <= :date AND end_date >= :date;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":date"},
			wantOccs: 2,
		},
		{
			name:     "positional question marks",
			sql:      "SELECT * FROM users WHERE id = ? AND status = ?;",
			engine:   database.SQLite,
			wantDefs: []string{"?1", "?2"},
			wantOccs: 2,
		},
		{
			name:     "sqlite numbered positional",
			sql:      "SELECT * FROM users WHERE id = ?1 OR parent_id = ?1 AND status = ?2;",
			engine:   database.SQLite,
			wantDefs: []string{"?1", "?2"},
			wantOccs: 3,
		},
		{
			name:     "numbered dollar parameters",
			sql:      "SELECT * FROM users WHERE id = $1 AND name = $2 OR supervisor_id = $1;",
			engine:   database.PostgreSQL,
			wantDefs: []string{"$1", "$2"},
			wantOccs: 3,
		},
		{
			name:     "at-sign parameters",
			sql:      "SELECT * FROM dataset.table WHERE circle = @circle AND min_ga > @min_ga;",
			engine:   database.BigQuery,
			wantDefs: []string{"@circle", "@min_ga"},
			wantOccs: 2,
		},
		{
			name:     "sqlite dollar named parameters",
			sql:      "SELECT * FROM t WHERE col = $my_col;",
			engine:   database.SQLite,
			wantDefs: []string{"$my_col"},
			wantOccs: 1,
		},
		{
			name:     "ignores postgres type casts",
			sql:      "SELECT id::text, count(*)::integer FROM users WHERE age > :min_age;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":min_age"},
			wantOccs: 1,
		},
		{
			name:     "parameter followed by cast",
			sql:      "SELECT * FROM t WHERE created_at > :start::date;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":start"},
			wantOccs: 1,
		},
		{
			name:     "ignores assignment operator",
			sql:      "SET @val := 10; SELECT * FROM t WHERE id = :id;",
			engine:   database.MySQL,
			wantDefs: []string{"@val", ":id"},
			wantOccs: 2,
		},
		{
			name:     "ignores system variables",
			sql:      "SELECT @@version, @@time_zone, :id;",
			engine:   database.MySQL,
			wantDefs: []string{":id"},
			wantOccs: 1,
		},
		{
			name:     "ignores inside string literals",
			sql:      "SELECT 'hello :not_param and ? and $1' AS greeting WHERE id = :id;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":id"},
			wantOccs: 1,
		},
		{
			name:     "ignores inside comments",
			sql:      "-- filter by :commented and ?\n/* block :block_param and $2 */\nSELECT * FROM t WHERE status = :status;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":status"},
			wantOccs: 1,
		},
		{
			name:     "ignores inside postgres dollar quotes",
			sql:      "SELECT $$inside dollar quote :ignored ? $1$$ FROM t WHERE real_id = :real_id;",
			engine:   database.PostgreSQL,
			wantDefs: []string{":real_id"},
			wantOccs: 1,
		},
		{
			name:     "ignores postgres jsonb operators",
			sql:      "SELECT * FROM t WHERE data ?| array['a'] AND id = ?;",
			engine:   database.PostgreSQL,
			wantDefs: []string{"?1"},
			wantOccs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			occs, defs := extractParameters(tc.sql, tc.engine)
			if len(occs) != tc.wantOccs {
				t.Fatalf("expected %d occurrences, got %d", tc.wantOccs, len(occs))
			}
			if len(defs) != len(tc.wantDefs) {
				t.Fatalf("expected %d defs (%v), got %d (%v)", len(tc.wantDefs), tc.wantDefs, len(defs), defs)
			}
			for i, want := range tc.wantDefs {
				if defs[i].Key != want {
					t.Errorf("def[%d]: expected key %s, got %s", i, want, defs[i].Key)
				}
			}
		})
	}
}

func TestFormatParamValue(t *testing.T) {
	// Auto mode numbers
	val, err := formatParamValue(queryParamValue{Value: "42", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "42" {
		t.Fatalf("expected '42', got %q, err %v", val, err)
	}
	val, err = formatParamValue(queryParamValue{Value: "-15.75", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "-15.75" {
		t.Fatalf("expected '-15.75', got %q, err %v", val, err)
	}

	// Auto mode leading zeros preserved as string
	val, err = formatParamValue(queryParamValue{Value: "0123", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "'0123'" {
		t.Fatalf("expected \"'0123'\", got %q, err %v", val, err)
	}

	// Auto mode strings
	val, err = formatParamValue(queryParamValue{Value: "JAYA", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "'JAYA'" {
		t.Fatalf("expected \"'JAYA'\", got %q, err %v", val, err)
	}

	// Single quote escaping
	val, err = formatParamValue(queryParamValue{Value: "O'Reilly", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "'O''Reilly'" {
		t.Fatalf("expected \"'O''Reilly'\", got %q, err %v", val, err)
	}

	// MySQL backslash escaping
	val, err = formatParamValue(queryParamValue{Value: `C:\data`, Type: "String"}, database.MySQL)
	if err != nil || val != `'C:\\data'` {
		t.Fatalf(`expected "'C:\\data'", got %q, err %v`, val, err)
	}

	// Booleans
	val, err = formatParamValue(queryParamValue{Value: "true", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "TRUE" {
		t.Fatalf("expected 'TRUE', got %q, err %v", val, err)
	}
	val, err = formatParamValue(queryParamValue{Value: "false", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "FALSE" {
		t.Fatalf("expected 'FALSE', got %q, err %v", val, err)
	}

	// Null
	val, err = formatParamValue(queryParamValue{IsNull: true}, database.PostgreSQL)
	if err != nil || val != "NULL" {
		t.Fatalf("expected 'NULL', got %q, err %v", val, err)
	}
	val, err = formatParamValue(queryParamValue{Value: "null", Type: "Auto"}, database.PostgreSQL)
	if err != nil || val != "NULL" {
		t.Fatalf("expected 'NULL', got %q, err %v", val, err)
	}

	// Raw mode
	val, err = formatParamValue(queryParamValue{Value: "CURRENT_TIMESTAMP", Type: "Raw"}, database.PostgreSQL)
	if err != nil || val != "CURRENT_TIMESTAMP" {
		t.Fatalf("expected 'CURRENT_TIMESTAMP', got %q, err %v", val, err)
	}

	// Number validation error
	_, err = formatParamValue(queryParamValue{Raw: ":id", Value: "abc", Type: "Number"}, database.PostgreSQL)
	if err == nil {
		t.Fatal("expected error for invalid number")
	}
}

func TestSubstituteParameters(t *testing.T) {
	sql := "SELECT * FROM sales WHERE circle = :circle AND ga_count > :min_ga AND region = :circle;"
	engine := database.PostgreSQL
	occs, _ := extractParameters(sql, engine)

	values := map[string]string{
		":circle": "'JAYA'",
		":min_ga": "100",
	}

	result := substituteParameters(sql, occs, values)
	expected := "SELECT * FROM sales WHERE circle = 'JAYA' AND ga_count > 100 AND region = 'JAYA';"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestQueryParametersDialogRunAndMemory(t *testing.T) {
	ui.RegisterFont(monoFont, "JetBrains Mono")
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	a := &App{
		client:        client,
		connection:    "SQLite Test",
		hideResults:   true,
		hideSearch:    true,
		explorerWidth: 260,
		editorPercent: 60,
	}

	query := "SELECT :val AS result, :count AS n;"
	a.addQuery(query)
	tab := a.currentEditor()

	v := ui.NewTester(a.view, 1180, 780)
	v.SetDark(true)

	// Trigger run
	a.run(false)
	if !a.showQueryParams {
		t.Fatal("expected showQueryParams to be true")
	}
	v.Frame()

	// Dialog should show up
	if !v.HasText("Query Parameters") {
		t.Fatal("expected Query Parameters dialog to be rendered")
	}
	if !v.HasText("Parameter :val") || !v.HasText("Parameter :count") {
		t.Fatal("expected parameter labels")
	}

	// Set parameter values
	a.queryParams.params[0].Value = "Hello World"
	a.queryParams.params[1].Value = "42"

	// Submit dialog
	a.submitQueryParams()
	if a.showQueryParams {
		t.Fatal("expected showQueryParams to be false after submission")
	}

	// Wait for query to complete
	deadline := time.Now().Add(3 * time.Second)
	for a.queryBusy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Verify values were remembered in tab.paramValues
	if tab.paramValues == nil || tab.paramValues[":val"].Value != "Hello World" || tab.paramValues[":count"].Value != "42" {
		t.Fatalf("parameters were not remembered: %+v", tab.paramValues)
	}

	// Run again on the same tab - should pre-populate remembered values
	a.run(false)
	if !a.showQueryParams {
		t.Fatal("expected showQueryParams to be true on second run")
	}
	if a.queryParams.params[0].Value != "Hello World" || a.queryParams.params[1].Value != "42" {
		t.Fatalf("expected pre-populated values, got: %+v", a.queryParams.params)
	}
	a.showQueryParams = false
}

func TestQueryParametersDialogValidation(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	a := &App{client: client, connection: "Test"}
	a.addQuery("SELECT * FROM t WHERE id = :id;")
	a.run(false)

	if !a.showQueryParams {
		t.Fatal("expected dialog to open")
	}

	// Set invalid number with Number type
	a.queryParams.params[0].Type = "Number"
	a.queryParams.params[0].Value = "not-a-number"

	a.submitQueryParams()
	// Should stay open with error
	if !a.showQueryParams {
		t.Fatal("dialog should stay open on validation error")
	}
	if !strings.Contains(a.queryParams.errorText, "invalid number") {
		t.Fatalf("expected invalid number error, got %q", a.queryParams.errorText)
	}

	// Fix value
	a.queryParams.params[0].Value = "123"
	a.submitQueryParams()
	if a.showQueryParams {
		t.Fatal("dialog should close after valid submission")
	}
}

func TestQueryParametersToolbarAction(t *testing.T) {
	ctx := context.Background()
	client, err := database.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	a := &App{client: client, connection: "Test"}
	a.addQuery("SELECT 1;")
	a.openQueryParamsDialog()
	if a.showQueryParams {
		t.Fatal("should not open dialog for query without parameters")
	}
	if !strings.Contains(a.status, "No parameters found") {
		t.Fatalf("expected status message, got %q", a.status)
	}

	a.addQuery("SELECT :name;")
	a.openQueryParamsDialog()
	if !a.showQueryParams {
		t.Fatal("should open dialog for query with parameters")
	}
}
