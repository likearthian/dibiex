package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

func formatCell(value any) string {
	text := formatCellText(value)
	if len(text) > maxCellBytes {
		text = strings.ToValidUTF8(text[:maxCellBytes], "") + "…"
	}
	return text
}

func formatCellText(value any) string {
	var text string
	switch v := value.(type) {
	case nil:
		text = "NULL"
	case []byte:
		text = fmt.Sprintf("<BLOB %d bytes>", len(v))
	case time.Time:
		text = v.Format(time.RFC3339Nano)
	case *big.Rat:
		text = strings.TrimRight(strings.TrimRight(v.FloatString(38), "0"), ".")
	case []bigquery.Value, map[string]bigquery.Value:
		data, err := json.Marshal(v)
		if err == nil {
			text = string(data)
		} else {
			text = fmt.Sprint(v)
		}
	default:
		text = fmt.Sprint(v)
	}
	return text
}
func (c *Client) bigQueryQuery(statement string) *bigquery.Query {
	q := c.bq.Query(statement)
	q.UseLegacySQL = false
	if c.config.Dataset != "" {
		q.DefaultProjectID, q.DefaultDatasetID = c.config.dataProject(), c.config.Dataset
	}
	q.Location = c.config.Location
	q.MaxBytesBilled = c.config.MaxBytesBilled
	return q
}
func (c *Client) startBigQuery(ctx context.Context, statement string) (*bigquery.Job, error) {
	return c.bigQueryQuery(statement).Run(ctx)
}
func cancelBigQueryOnError(ctx context.Context, job *bigquery.Job) {
	if ctx.Err() == nil {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = job.Cancel(cancelCtx)
}
func (c *Client) queryBigQuery(ctx context.Context, statement string) (out Result, err error) {
	return c.queryBigQueryBounded(ctx, statement, MaxRows, maxCellBytes, maxResultBytes)
}

func (c *Client) queryBigQueryBounded(ctx context.Context, statement string, maxRows, cellBytes, resultBytes int) (out Result, err error) {
	start := time.Now()
	defer func() { out.Elapsed = time.Since(start) }()
	job, err := c.startBigQuery(ctx, statement)
	if err != nil {
		return out, fmt.Errorf("start BigQuery job: %w", err)
	}
	defer cancelBigQueryOnError(ctx, job)
	rows, err := job.Read(ctx)
	if err != nil {
		return out, fmt.Errorf("read BigQuery job: %w", err)
	}
	rows.PageInfo().MaxSize = MaxRows + 1
	for _, field := range rows.Schema {
		out.Columns = append(out.Columns, field.Name)
		kind := string(field.Type)
		if field.Repeated {
			kind += "[]"
		}
		out.ColumnTypes = append(out.ColumnTypes, kind)
	}
	totalBytes := 0
	for {
		var values []bigquery.Value
		err = rows.Next(&values)
		if err == iterator.Done {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("read BigQuery rows: %w", err)
		}
		if len(out.Rows) == maxRows {
			out.Truncated = true
			return out, nil
		}
		row := make([]string, len(values))
		for i, v := range values {
			if cellBytes == maxCellBytes {
				row[i] = formatCell(v)
			} else {
				if v == nil {
					row[i] = "NULL"
				} else {
					row[i] = fmt.Sprint(v)
				}
				if len(row[i]) > cellBytes {
					return out, fmt.Errorf("schema metadata cell exceeds the 1 MiB limit")
				}
			}
			totalBytes += len(row[i])
		}
		if totalBytes > resultBytes {
			out.Truncated = true
			return out, nil
		}
		out.Rows = append(out.Rows, row)
	}
}
func (c *Client) bigQueryTables(ctx context.Context, schema Schema) ([]Table, error) {
	var out []Table
	token := ""
	for {
		page, err := c.bqAPI.Tables.List(schema.Project, schema.Name).Context(ctx).MaxResults(MaxRows + 1).PageToken(token).Do()
		if err != nil {
			return nil, fmt.Errorf("list BigQuery tables: %w", err)
		}
		for _, table := range page.Tables {
			if len(out) == CatalogLimit {
				return nil, fmt.Errorf("dataset exceeds the 10,000 object display limit")
			}
			ref := table.TableReference
			if ref == nil {
				continue
			}
			partition := bigQueryPartition(table.TimePartitioning, table.RangePartitioning, table.RequirePartitionFilter)
			kind := bigQueryKind(table.Type, partition)
			var clustering []string
			if table.Clustering != nil {
				clustering = table.Clustering.Fields
			}
			out = append(out, Table{Project: ref.ProjectId, Schema: ref.DatasetId, Name: ref.TableId, Kind: kind, Partition: partition, Clustering: clustering})
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
