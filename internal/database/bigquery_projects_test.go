package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/option"
)

func TestBigQueryMultipleProjectCatalog(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	denied := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "projects" || parts[2] != "datasets" {
			t.Errorf("unexpected request: %s", r.URL)
			http.Error(w, "unexpected endpoint", http.StatusNotImplemented)
			return
		}
		project := parts[1]
		calls[project]++
		w.Header().Set("Content-Type", "application/json")
		if project == "other-project" && denied {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"code":403,"message":"permission denied"}}`)
			return
		}
		if project == "empty-project" {
			fmt.Fprint(w, `{"datasets":[]}`)
			return
		}
		name := "shared"
		next := "page-2"
		if r.URL.Query().Get("pageToken") == "page-2" {
			name, next = "second", ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nextPageToken": next,
			"datasets":      []any{map[string]any{"datasetReference": map[string]string{"projectId": project, "datasetId": name}}},
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bq, err := bigquery.NewClient(ctx, "billing-project", option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer bq.Close()
	cfg := Config{Engine: BigQuery, Project: "billing-project", DatasetProject: "data-project", AdditionalProjects: []string{"other-project", "empty-project"}}
	client := &Client{bq: bq, config: cfg}
	cache := t.TempDir()
	p := NewIntrospector(client, cache)
	schemas, err := p.Catalog(ctx, false)
	if err != nil || len(schemas) != 4 {
		t.Fatalf("multi-project catalog: %v, %v", schemas, err)
	}
	for _, schema := range schemas {
		if schema.Loaded || len(schema.Tables) > 0 {
			t.Fatal("listing projects fetched dataset contents")
		}
	}
	if schemas[1].ID() == schemas[3].ID() {
		t.Fatal("same-named datasets in different projects collided")
	}
	mu.Lock()
	if calls["data-project"] != 2 || calls["other-project"] != 2 || calls["empty-project"] != 1 || calls["billing-project"] != 0 {
		t.Errorf("unexpected paginated requests: %v", calls)
	}
	mu.Unlock()
	// A restart must reuse all three project scopes, including the empty one.
	reopened := NewIntrospector(client, cache)
	if _, err = reopened.Catalog(ctx, false); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if calls["data-project"] != 2 || calls["other-project"] != 2 || calls["empty-project"] != 1 {
		t.Errorf("restart did not reuse project cache: %v", calls)
	}
	denied = true
	mu.Unlock()
	otherTable := Table{Project: "other-project", Schema: "shared", Name: "events"}
	for i := range reopened.snapshot.Schemas {
		if reopened.snapshot.Schemas[i].Project == otherTable.Project && reopened.snapshot.Schemas[i].Name == otherTable.Schema {
			reopened.snapshot.Schemas[i].Loaded = true
			reopened.snapshot.Schemas[i].Tables = []Table{otherTable}
		}
	}
	reopened.snapshot.Metadata[otherTable.ID()] = TableMetadata{FetchedAt: time.Now()}
	before := cloneSchemaValue(reopened.snapshot)
	if _, err = reopened.BigQueryProject(ctx, "other-project", true); err == nil {
		t.Fatal("permission error was ignored")
	}
	if len(reopened.snapshot.Schemas) != len(before.Schemas) || !reopened.snapshot.ProjectAt["other-project"].Equal(before.ProjectAt["other-project"]) {
		t.Fatal("failed refresh replaced the last good snapshot")
	}
	if _, err = reopened.BigQueryProject(ctx, "data-project", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.snapshot.Metadata[otherTable.ID()]; !ok {
		t.Fatal("project refresh removed another project's metadata")
	}
	mu.Lock()
	if calls["data-project"] != 4 || calls["other-project"] != 3 {
		t.Errorf("project refresh crossed scopes: %v", calls)
	}
	mu.Unlock()
	if client.Settings().Project != "billing-project" || client.Settings().DatasetProject != "data-project" {
		t.Fatal("browsing changed query defaults")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := reopened.BigQueryProject(cancelled, "data-project", true); err == nil {
		t.Fatal("project refresh ignored cancellation")
	}
}

func TestBigQueryProjectConfigCompatibility(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"Engine":"BigQuery","Project":"billing","DatasetProject":"data"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.BigQueryProjects(), []string{"data"}) {
		t.Fatal("legacy configuration changed its data project")
	}
	cfg.AdditionalProjects = []string{"extra", "data", "extra"}
	if !slices.Equal(cfg.BigQueryProjects(), []string{"data", "extra"}) {
		t.Fatal("project list was not deduplicated")
	}
}

func TestForgetBigQueryProjectPreservesOtherCachedScopes(t *testing.T) {
	removed := Table{Project: "primary", Schema: "shared", Name: "events"}
	retained := removed
	retained.Project = "other"
	at := time.Now().UTC()
	p := &Introspector{snapshot: schemaSnapshot{Version: cacheVersion, Key: "test",
		Schemas:   []Schema{{Project: removed.Project}, {Project: retained.Project}},
		ProjectAt: map[string]time.Time{removed.Project: at, retained.Project: at},
		Metadata:  map[string]TableMetadata{removed.ID(): {}, retained.ID(): {}},
	}, path: t.TempDir() + "/cache.json"}
	p.ForgetBigQueryProject(removed.Project)
	reopened := &Introspector{path: p.path, snapshot: schemaSnapshot{Key: "test"}}
	reopened.read()
	if len(reopened.snapshot.Schemas) != 1 || reopened.snapshot.Schemas[0].Project != retained.Project || len(reopened.snapshot.ProjectAt) != 1 || len(reopened.snapshot.Metadata) != 1 {
		t.Fatal("cache removal crossed project scopes or did not persist")
	}
	if _, ok := reopened.snapshot.Metadata[retained.ID()]; !ok {
		t.Fatal("another project's metadata was removed")
	}
}
