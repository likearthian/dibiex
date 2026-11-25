package database

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"google.golang.org/api/iterator"
)

type Schema struct {
	Project, Name string
	Tables        []Table
	Loaded        bool
	FetchedAt     time.Time
	FromCache     bool `json:"-"`
}

func (s Schema) ID() string { return s.Project + "\x00" + s.Name }

func (c *Client) BigQueryAccessibleProjects(ctx context.Context) ([]string, error) {
	if c.bqAPI == nil {
		return nil, fmt.Errorf("project discovery requires a BigQuery connection")
	}
	var projects []string
	call := c.bqAPI.Projects.List().MaxResults(MaxRows).Context(ctx)
	for {
		page, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list BigQuery projects: %w", err)
		}
		for _, project := range page.Projects {
			if project.ProjectReference != nil {
				projects = append(projects, project.ProjectReference.ProjectId)
			}
		}
		if len(projects) > CatalogLimit || len(projects) == CatalogLimit && page.NextPageToken != "" {
			return nil, fmt.Errorf("project list exceeds the 10,000 project display limit")
		}
		if page.NextPageToken == "" {
			slices.Sort(projects)
			return slices.Compact(projects), nil
		}
		call.PageToken(page.NextPageToken)
	}
}
func (c *Client) Catalog(ctx context.Context) ([]Schema, error) {
	if c.bq != nil {
		var schemas []Schema
		for _, project := range c.config.BigQueryProjects() {
			items, err := c.BigQueryProjectDatasets(ctx, project)
			if err != nil {
				return nil, err
			}
			schemas = append(schemas, items...)
			if len(schemas) > CatalogLimit {
				return nil, fmt.Errorf("connection exceeds the 10,000 dataset display limit")
			}
		}
		return schemas, nil
	}
	return c.sqlCatalog(ctx)
}

func (c *Client) BigQueryProjectDatasets(ctx context.Context, project string) ([]Schema, error) {
	if c.bq == nil {
		return nil, fmt.Errorf("project browsing requires a BigQuery connection")
	}
	it := c.bq.Datasets(ctx)
	it.ProjectID = project
	it.PageInfo().MaxSize = MaxRows + 1
	var schemas []Schema
	for {
		dataset, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list datasets in %s: %w", project, err)
		}
		if len(schemas) == CatalogLimit {
			return nil, fmt.Errorf("project exceeds the 10,000 dataset display limit")
		}
		schemas = append(schemas, Schema{Project: dataset.ProjectID, Name: dataset.DatasetID})
	}
	// A directly accessible default dataset may not be included in project listing.
	if c.config.Dataset != "" && project == c.config.dataProject() {
		found := false
		for _, s := range schemas {
			if s.Name == c.config.Dataset {
				found = true
				break
			}
		}
		if !found {
			if len(schemas) == CatalogLimit {
				return nil, fmt.Errorf("project exceeds the 10,000 dataset display limit")
			}
			schemas = append(schemas, Schema{Project: c.config.dataProject(), Name: c.config.Dataset})
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Name < schemas[j].Name })
	return schemas, nil
}

func (c *Client) sqlCatalog(ctx context.Context) ([]Schema, error) {
	tables, err := c.Objects(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string][]Table{}
	if c.Engine() == SQLite {
		byName["main"] = nil
		names, e := c.catalogQuery(ctx, "SELECT name FROM pragma_database_list ORDER BY seq")
		if e != nil {
			return nil, e
		}
		for _, r := range names.Rows {
			byName[r[0]] = nil
		}
	} else if c.Engine() == MySQL {
		names, err := c.mysqlCatalogNames(ctx)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			byName[name] = nil
		}
	} else {
		names, err := c.catalogQuery(ctx, `SELECT nspname FROM pg_catalog.pg_namespace
 WHERE nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp_%' ORDER BY nspname`)
		if err != nil {
			return nil, err
		}
		if names.Truncated {
			return nil, fmt.Errorf("catalog exceeds the 10,000 schema display limit")
		}
		for _, row := range names.Rows {
			byName[row[0]] = nil
		}
	}
	for _, table := range tables {
		byName[table.Schema] = append(byName[table.Schema], table)
	}
	var out []Schema
	for name, tables := range byName {
		out = append(out, Schema{Name: name, Tables: tables, Loaded: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *Client) SchemaTables(ctx context.Context, schema Schema) ([]Table, error) {
	if c.bq != nil {
		return c.bigQueryTables(ctx, schema)
	}
	tables, err := c.Objects(ctx)
	if err != nil {
		return nil, err
	}
	var out []Table
	for _, table := range tables {
		if table.Schema == schema.Name {
			out = append(out, table)
		}
	}
	return out, nil
}
