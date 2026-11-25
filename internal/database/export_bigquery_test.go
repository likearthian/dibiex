package database

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	bqapi "google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
)

func TestBigQueryFullExportPagesBillingAndCancellation(t *testing.T) {
	var mu sync.Mutex
	dryRuns, executions, cancellations, pages := 0, 0, 0, 0
	statementType := "SELECT"
	schema := &bqapi.TableSchema{Fields: []*bqapi.TableFieldSchema{{Name: "id", Type: "INTEGER"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/jobs") && r.Method == "POST" {
			var job bqapi.Job
			if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
				t.Error(err)
				http.Error(w, "bad job", 400)
				return
			}
			q := job.Configuration.Query
			if q.MaximumBytesBilled != 1073741824 || q.DefaultDataset.DatasetId != "dataset" || job.JobReference.Location != "US" {
				t.Error("export lost billing cap, default dataset or location")
			}
			if !strings.HasPrefix(q.Query, "SELECT id FROM source ORDER BY id") {
				t.Error("export rewrote query ordering")
			}
			if job.Configuration.DryRun {
				dryRuns++
			} else {
				executions++
			}
			job.Status = &bqapi.JobStatus{State: "DONE"}
			job.Statistics = &bqapi.JobStatistics{Query: &bqapi.JobStatistics2{StatementType: statementType}}
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			cancellations++
			_, _ = io.WriteString(w, `{"job":{"status":{"state":"DONE"}}}`)
			return
		}
		if strings.Contains(r.URL.Path, "/queries/") {
			response := bqapi.GetQueryResultsResponse{JobComplete: true, TotalRows: 2505, Schema: schema}
			if r.URL.Query().Get("maxResults") != "0" {
				pages++
				start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
				end := min(2505, start+1000)
				for i := start; i < end; i++ {
					response.Rows = append(response.Rows, &bqapi.TableRow{F: []*bqapi.TableCell{{V: strconv.Itoa(i + 1)}}})
				}
				if end < 2505 {
					response.PageToken = strconv.Itoa(end)
				}
			}
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		t.Errorf("unexpected BigQuery request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected endpoint", 404)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bq, err := bigquery.NewClient(ctx, "billing", option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{bq: bq, config: Config{Engine: BigQuery, Project: "billing", Dataset: "dataset", Location: "US", MaxBytesBilled: 1073741824}}
	defer c.Close()
	query := "SELECT id FROM source ORDER BY id"
	count := 0
	err = c.StreamExportQuery(ctx, query, func(columns []string, next func() ([]string, error)) error {
		if len(columns) != 1 || columns[0] != "id" {
			t.Fatal("wrong schema")
		}
		for {
			row, e := next()
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			count++
			if row[0] != strconv.Itoa(count) {
				t.Fatal("paged export lost order")
			}
		}
	})
	if err != nil || count != 2505 {
		t.Fatalf("BigQuery export stopped early: %d %v", count, err)
	}
	mu.Lock()
	if dryRuns != 1 || executions != 1 || pages != 3 {
		t.Errorf("wrong full-export requests: dry=%d run=%d pages=%d", dryRuns, executions, pages)
	}
	mu.Unlock()
	sink := errors.New("disk full")
	err = c.StreamExportQuery(ctx, query, func([]string, func() ([]string, error)) error { return sink })
	if !errors.Is(err, sink) {
		t.Fatal("lost downstream export error", err)
	}
	mu.Lock()
	if cancellations != 1 {
		t.Error("failed export did not cancel remote job")
	}
	statementType = "INSERT"
	before := executions
	mu.Unlock()
	err = c.StreamExportQuery(ctx, query, func([]string, func() ([]string, error)) error { return nil })
	if err == nil {
		t.Fatal("non-SELECT validation was ignored")
	}
	mu.Lock()
	if executions != before {
		t.Error("non-SELECT validation started a real job")
	}
	mu.Unlock()
}
