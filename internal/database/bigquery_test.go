package database

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	bqapi "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
)

func TestBigQuerySimulation(t *testing.T) {
	endpoint := os.Getenv("DBCRAFT_BIGQUERY_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DBCRAFT_BIGQUERY_ENDPOINT to the local Postman simulation endpoint")
	}
	initialMetadataJobs := simulationMetadataJobs(t, endpoint)
	ctx, finish := context.WithTimeout(context.Background(), 20*time.Second)
	defer finish()
	bq, err := bigquery.NewClient(ctx, "test-project", option.WithEndpoint(strings.TrimRight(endpoint, "/")+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	api, err := bqapi.NewService(ctx, option.WithEndpoint(strings.TrimRight(endpoint, "/")+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{bq: bq, bqAPI: api, config: Config{Engine: BigQuery, Project: "test-project", Dataset: "test_dataset", Location: "US", MaxBytesBilled: 1073741824}}
	defer c.Close()
	if _, err := bq.Dataset("test_dataset").Metadata(ctx); err != nil {
		t.Fatal(err)
	}
	objects, err := c.Objects(ctx)
	if err != nil || len(objects) != 2 || objects[0].Name != "events" || objects[1].Name != "orders" {
		t.Fatalf("paginated catalog: %+v %v", objects, err)
	}
	if objects[1].Kind != "Views" {
		t.Fatalf("view kind lost: %+v", objects[1])
	}
	c.config.Dataset = ""
	if err := c.config.Validate(); err != nil {
		t.Fatalf("dataset should be optional: %v", err)
	}
	schemas, err := c.Catalog(ctx)
	if err != nil || len(schemas) != 3 {
		t.Fatalf("project datasets: %+v %v", schemas, err)
	}
	for _, schema := range schemas {
		if schema.Loaded || len(schema.Tables) != 0 {
			t.Fatal("dataset tables must load on expansion")
		}
	}
	if schemas[0].Name != "empty_dataset" || schemas[1].Name != "restricted_dataset" || schemas[2].Name != "test_dataset" {
		t.Fatalf("dataset ordering: %+v", schemas)
	}
	if _, err := c.SchemaTables(ctx, schemas[1]); err == nil {
		t.Fatal("expected dataset permission error")
	}
	if tables, err := c.SchemaTables(ctx, schemas[0]); err != nil || len(tables) != 0 {
		t.Fatalf("empty dataset: %v %v", tables, err)
	}
	if _, err := c.Query(ctx, "SELECT * FROM `test-project.test_dataset.events` -- project-only"); err != nil {
		t.Fatalf("project-level query: %v", err)
	}
	c.config.Dataset = "test_dataset"
	out, err := c.Query(ctx, "types")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Columns) != 5 || len(out.Rows) != 1 || out.Rows[0][1] != "NULL" || out.Rows[0][2] != "<BLOB 2 bytes>" || out.Rows[0][3] != `["one","two"]` || out.Rows[0][4] != "123.456789123" {
		t.Fatalf("typed result: %+v", out)
	}
	out, err = c.Query(ctx, "large")
	if err != nil || len(out.Rows) != MaxRows || !out.Truncated {
		t.Fatalf("paginated row limit: %d %v %v", len(out.Rows), out.Truncated, err)
	}
	for _, sql := range []string{"invalid", "denied"} {
		if _, err = c.Query(ctx, sql); err == nil {
			t.Fatalf("expected API error for %s", sql)
		}
	}
	if _, err = c.Execute(ctx, "write"); err != nil {
		t.Fatal(err)
	}
	affected, err := c.ExecuteResult(ctx, "write")
	if err != nil || affected.RowsAffected != 3 {
		t.Fatalf("DML statistics: %+v %v", affected, err)
	}
	metadata, err := c.TableMetadata(ctx, Table{Project: "test-project", Schema: "test_dataset", Name: "events"})
	if err != nil || len(metadata.Columns) != 5 || !metadata.Columns[0].PrimaryKey || metadata.RowEstimate != "≈ 42" || !strings.Contains(metadata.DDL, "CREATE TABLE") {
		t.Fatalf("table metadata: %+v %v", metadata, err)
	}
	if metadata.Kind != "Partitioned tables" || !metadata.Columns[1].Partitioning || metadata.Columns[0].ClusteringOrdinal != 1 || metadata.Columns[2].ClusteringOrdinal != 2 || !metadata.Columns[2].ForeignKey || metadata.Columns[3].Fields[0].Fields[0].Name != "city" || metadata.Columns[4].Type != "ARRAY<RECORD>" {
		t.Fatalf("rich metadata: %+v", metadata)
	}
	if metadata.Columns[2].References[0].Table != "accounts" {
		t.Fatal("foreign key target lost")
	}
	for _, name := range []string{"ingestion", "range_events", "orders"} {
		m, e := c.TableMetadata(ctx, Table{Project: "test-project", Schema: "test_dataset", Name: name})
		if e != nil {
			t.Fatal(e)
		}
		if name == "ingestion" && (!m.Partition.IngestionTime || len(m.Columns) != 7 || !m.Columns[5].Partitioning) {
			t.Fatalf("ingestion partition: %+v", m)
		}
		if name == "range_events" && (m.Partition.RangeInterval != "100" || !m.Columns[0].Partitioning) {
			t.Fatalf("range partition: %+v", m)
		}
		if name == "orders" && (m.Kind != "Views" || !strings.Contains(m.DDL, "CREATE VIEW")) {
			t.Fatalf("view: %+v", m)
		}
	}
	for _, name := range []string{"denied_metadata", "missing_metadata"} {
		if _, e := c.TableMetadata(ctx, Table{Project: "test-project", Schema: "test_dataset", Name: name}); e == nil {
			t.Fatalf("expected metadata error for %s", name)
		}
	}
	short, stopMetadata := context.WithTimeout(ctx, 50*time.Millisecond)
	if _, e := c.TableMetadata(short, Table{Project: "test-project", Schema: "test_dataset", Name: "slow_metadata"}); e == nil {
		t.Fatal("metadata read ignored deadline")
	}
	stopMetadata()
	if _, err = c.TableMetadata(ctx, Table{Project: "test-project", Schema: "test_dataset", Name: "denied_metadata"}); err == nil {
		t.Fatal("expected metadata permission error")
	}
	cacheDir := t.TempDir()
	pipeline := NewIntrospector(c, cacheDir)
	if _, e := pipeline.Catalog(ctx, false); e != nil {
		t.Fatal(e)
	}
	if _, e := pipeline.Schema(ctx, Schema{Project: "test-project", Name: "test_dataset"}, false); e != nil {
		t.Fatal(e)
	}
	table := Table{Project: "test-project", Schema: "test_dataset", Name: "events"}
	if _, e := pipeline.Table(ctx, table, false); e != nil {
		t.Fatal(e)
	}
	pipeline = NewIntrospector(c, cacheDir)
	cached, e := pipeline.Table(ctx, table, false)
	if e != nil || !cached.FromCache || cached.Columns[3].Fields[0].Fields[0].Name != "city" {
		t.Fatalf("REST metadata cache restart: %+v %v", cached, e)
	}
	deadline, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err = c.Query(deadline, "slow"); err == nil {
		t.Fatal("expected timeout")
	}
	// Inspect the simulation's remote state to prove that timeout requests jobs.cancel.
	stateURL := strings.TrimSuffix(endpoint, "/") + "/__state"
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(stateURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state struct {
		MetadataReads []string `json:"metadataReads"`
		Jobs          []struct {
			Cancelled     bool `json:"cancelled"`
			Configuration struct {
				Query struct {
					Query string `json:"query"`
				} `json:"query"`
			} `json:"configuration"`
		} `json:"jobs"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.MetadataReads) < 8 {
		t.Fatal("table metadata REST path was not exercised")
	}
	metadataJobs := 0
	for _, job := range state.Jobs {
		if strings.Contains(job.Configuration.Query.Query, "INFORMATION_SCHEMA") {
			metadataJobs++
		}
	}
	if metadataJobs != initialMetadataJobs {
		t.Fatal("base metadata used a query job instead of the table API")
	}
	if len(state.Jobs) == 0 || !state.Jobs[len(state.Jobs)-1].Cancelled {
		t.Fatal("timed out job was not cancelled remotely")
	}
	if metadata.Sections["indexes"].State != "unloaded" {
		t.Fatal("expensive metadata must load explicitly")
	}
	properties, _ := json.Marshal(metadata.Properties)
	for _, value := range []string{"analytics", "expirationTime", "kmsKeyName"} {
		if !strings.Contains(string(properties), value) {
			t.Fatalf("provider table property missing %s", value)
		}
	}
	for _, section := range []string{"indexes", "partitions", "policies", "privileges"} {
		m, e := pipeline.Section(ctx, table, section, false)
		if e != nil {
			t.Fatalf("%s: %v", section, e)
		}
		want := 1
		if section == "indexes" {
			want = 2
		}
		if len(m.SectionItems(section)) != want || m.Sections[section].State != "loaded" {
			t.Fatalf("%s metadata: %+v", section, m)
		}
	}
	// The expanded sections survive restart, including IAM condition details.
	pipeline = NewIntrospector(c, cacheDir)
	cached, e = pipeline.Table(ctx, table, false)
	if e != nil || !cached.FromCache || len(cached.Indexes) != 2 || len(cached.Partitions) != 1 {
		t.Fatalf("section persistence: %+v %v", cached, e)
	}
	privileges, _ := json.Marshal(cached.Privileges)
	if !strings.Contains(string(privileges), "limited") {
		t.Fatal("IAM conditions lost")
	}
}

func simulationMetadataJobs(t *testing.T, endpoint string) int {
	t.Helper()
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get(strings.TrimRight(endpoint, "/") + "/__state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state struct {
		Jobs []struct {
			Configuration struct{ Query struct{ Query string } }
		}
	}
	if err = json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range state.Jobs {
		if strings.Contains(job.Configuration.Query.Query, "INFORMATION_SCHEMA") {
			count++
		}
	}
	return count
}
