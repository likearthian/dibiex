package database

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const MaxBigQueryProjects = 16

func (cfg Config) BigQueryProjects() []string {
	projects := []string{}
	seen := map[string]bool{}
	for _, project := range append([]string{cfg.dataProject()}, cfg.AdditionalProjects...) {
		project = strings.TrimSpace(project)
		if project != "" && !seen[project] && !slices.Contains(cfg.HiddenProjects, project) {
			projects = append(projects, project)
			seen[project] = true
		}
	}
	return projects
}

func (p *Introspector) ForgetBigQueryProject(project string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot.Schemas = slices.DeleteFunc(p.snapshot.Schemas, func(s Schema) bool { return s.Project == project })
	delete(p.snapshot.ProjectAt, project)
	for id := range p.snapshot.Metadata {
		if strings.HasPrefix(id, project+"\x00") {
			delete(p.snapshot.Metadata, id)
		}
	}
	p.save()
}

func ValidateBigQueryProject(project string) error {
	if project == "" || len(project) > 128 {
		return fmt.Errorf("enter a Google Cloud project ID")
	}
	for _, r := range project {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-.:", r)) {
			return fmt.Errorf("enter a project ID, not a project name or URL")
		}
	}
	return nil
}

func (p *Introspector) BigQueryCatalog(ctx context.Context, projects []string, force bool) ([]Schema, error) {
	if len(projects) > MaxBigQueryProjects {
		return nil, fmt.Errorf("a connection supports up to %d projects", MaxBigQueryProjects)
	}
	var schemas []Schema
	for _, project := range projects {
		items, err := p.BigQueryProject(ctx, project, force)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, items...)
	}
	return schemas, nil
}

// BigQueryProject refreshes only this project's datasets, retaining other scopes
// and the last good snapshot if discovery fails.
func (p *Introspector) BigQueryProject(ctx context.Context, project string, force bool) ([]Schema, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateBigQueryProject(project); err != nil {
		return nil, err
	}
	var previous, others []Schema
	for _, schema := range p.snapshot.Schemas {
		if schema.Project == project {
			previous = append(previous, schema)
		} else {
			others = append(others, schema)
		}
	}
	if !force && fresh(p.snapshot.ProjectAt[project]) {
		out := cloneSchemaValue(previous)
		for i := range out {
			out[i].FromCache = true
		}
		return out, nil
	}
	source, ok := p.source.(interface {
		BigQueryProjectDatasets(context.Context, string) ([]Schema, error)
	})
	if !ok {
		return nil, fmt.Errorf("project browsing requires a BigQuery connection")
	}
	schemas, err := source.BigQueryProjectDatasets(ctx, project)
	if err != nil {
		return nil, err
	}
	if len(others)+len(schemas) > CatalogLimit {
		return nil, fmt.Errorf("connection exceeds the 10,000 dataset display limit")
	}
	for i := range schemas {
		if !force {
			for _, old := range previous {
				if old.ID() == schemas[i].ID() && old.Loaded && fresh(old.FetchedAt) {
					schemas[i] = cloneSchemaValue(old)
					schemas[i].FromCache = true
				}
			}
		}
	}
	keep := map[string]bool{}
	for _, schema := range schemas {
		for _, table := range schema.Tables {
			keep[table.ID()] = true
		}
	}
	for id := range p.snapshot.Metadata {
		if strings.HasPrefix(id, project+"\x00") && !keep[id] {
			delete(p.snapshot.Metadata, id)
		}
	}
	if p.snapshot.ProjectAt == nil {
		p.snapshot.ProjectAt = map[string]time.Time{}
	}
	p.snapshot.ProjectAt[project] = time.Now().UTC()
	p.snapshot.Schemas = append(cloneSchemaValue(others), cloneSchemaValue(schemas)...)
	p.save()
	return cloneSchemaValue(schemas), nil
}
