package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const cacheVersion = 2
const cacheTTL = 30 * 24 * time.Hour
const maxCacheBytes = 32 << 20
const maxCachedTables = 256

type catalogSource interface {
	Catalog(context.Context) ([]Schema, error)
	SchemaTables(context.Context, Schema) ([]Table, error)
	TableMetadata(context.Context, Table) (TableMetadata, error)
}

type schemaSnapshot struct {
	Version   int
	Key       string
	CatalogAt time.Time
	Schemas   []Schema
	Metadata  map[string]TableMetadata
	Explorer  *ExplorerCatalog
	ProjectAt map[string]time.Time `json:",omitempty"`
}

// Introspector methods perform IO and must run on workers, never UI callbacks.
type Introspector struct {
	mu       sync.Mutex
	source   catalogSource
	path     string
	snapshot schemaSnapshot
	warning  string
}

func NewIntrospector(client *Client, cacheDir string) *Introspector {
	cfg := client.Settings()
	if cfg.Engine == SQLite && cfg.Path == "" {
		cacheDir = ""
	}
	if cfg.Engine == SQLite && cfg.Path != "" {
		if path, err := filepath.Abs(cfg.Path); err == nil {
			cfg.Path = filepath.Clean(path)
		}
	}
	cfg.Password = ""
	cfg.AdditionalProjects = nil
	cfg.HiddenProjects = nil
	cfg.ExplorerHiddenProjects, cfg.ExplorerHiddenSchemas = nil, nil
	data, _ := json.Marshal(cfg)
	hash := sha256.Sum256(data)
	p := &Introspector{source: client, snapshot: schemaSnapshot{Version: cacheVersion, Key: hex.EncodeToString(hash[:]), Metadata: map[string]TableMetadata{}}}
	if cacheDir != "" {
		p.path = filepath.Join(cacheDir, p.snapshot.Key+".json")
		p.read()
	}
	return p
}

func DefaultSchemaCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DBCraft", "schema-v2"), nil
}

func cloneSchemaValue[T any](value T) T {
	data, _ := json.Marshal(value)
	var copy T
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (p *Introspector) Warning() string { p.mu.Lock(); defer p.mu.Unlock(); return p.warning }
func fresh(at time.Time) bool {
	age := time.Since(at)
	return !at.IsZero() && age >= 0 && age < cacheTTL
}

func (m TableMetadata) Fresh() bool { return fresh(m.FetchedAt) }

// CachedTables returns independent copies of the fresh table inspections.
func (p *Introspector) CachedTables() map[string]TableMetadata {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]TableMetadata)
	for id, metadata := range p.snapshot.Metadata {
		if metadata.Fresh() {
			metadata.FromCache = true
			out[id] = cloneSchemaValue(metadata)
		}
	}
	return out
}

// Inspect warms the snapshot with base table metadata. Optional sections
// still load on demand, especially BigQuery's billable metadata queries.
func (p *Introspector) Inspect(ctx context.Context, schemas []Schema) {
	if client, ok := p.source.(*Client); ok && client.Engine() == BigQuery {
		return
	}
	remaining := maxCachedTables
	for i, schema := range schemas {
		if ctx.Err() != nil || remaining == 0 || i >= maxCachedTables {
			return
		}
		if client, ok := p.source.(*Client); ok && client.Engine() == SQLite && schema.Name != "main" {
			continue
		}
		tables := schema.Tables
		if !schema.Loaded {
			var err error
			tables, err = p.Schema(ctx, schema, false)
			if err != nil {
				continue
			}
		}
		for _, table := range tables {
			if ctx.Err() != nil || remaining == 0 {
				return
			}
			remaining--
			_, _ = p.Table(ctx, table, false)
		}
	}
}

// Invalidate expires persisted values after a successful DDL/write execution.
func (p *Introspector) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot.CatalogAt = time.Time{}
	p.snapshot.ProjectAt = nil
	p.snapshot.Explorer = nil
	for i := range p.snapshot.Schemas {
		p.snapshot.Schemas[i].FetchedAt = time.Time{}
	}
	for id, m := range p.snapshot.Metadata {
		m.FetchedAt = time.Time{}
		p.snapshot.Metadata[id] = m
	}
	p.save()
}

func (p *Introspector) Catalog(ctx context.Context, force bool) ([]Schema, error) {
	if client, ok := p.source.(*Client); ok && client.Engine() == BigQuery {
		return p.BigQueryCatalog(ctx, client.Settings().BigQueryProjects(), force)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !force && fresh(p.snapshot.CatalogAt) {
		// SQLite attachments are session-local, so its roots are always discovered.
		if client, ok := p.source.(*Client); !ok || client.Engine() != SQLite {
			out := cloneSchemaValue(p.snapshot.Schemas)
			for i := range out {
				out[i].FromCache = true
			}
			return out, nil
		}
	}
	schemas, err := p.source.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range schemas {
		if schemas[i].Loaded {
			schemas[i].FetchedAt = now
		}
	}
	p.snapshot.CatalogAt = now
	// Root refresh invalidates descendants even when object names are unchanged.
	// BigQuery datasets remain unloaded until explicitly expanded.
	if force {
		p.snapshot.Metadata = map[string]TableMetadata{}
		p.snapshot.Explorer = nil
	} else {
		keep := map[string]bool{}
		for i := range schemas {
			if !schemas[i].Loaded {
				for _, old := range p.snapshot.Schemas {
					if old.ID() == schemas[i].ID() && old.Loaded && fresh(old.FetchedAt) {
						schemas[i] = cloneSchemaValue(old)
						schemas[i].FromCache = true
					}
				}
			}
			for _, t := range schemas[i].Tables {
				keep[t.ID()] = true
			}
		}
		for id := range p.snapshot.Metadata {
			if !keep[id] {
				delete(p.snapshot.Metadata, id)
			}
		}
	}
	p.snapshot.Schemas = cloneSchemaValue(schemas)
	p.save()
	return cloneSchemaValue(schemas), nil
}

func (p *Introspector) Schema(ctx context.Context, s Schema, force bool) ([]Table, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, cached := range p.snapshot.Schemas {
		if cached.ID() == s.ID() && cached.Loaded && !force && fresh(cached.FetchedAt) {
			return cloneSchemaValue(cached.Tables), nil
		}
	}
	tables, err := p.source.SchemaTables(ctx, s)
	if err != nil {
		return nil, err
	}
	for i := range p.snapshot.Schemas {
		if p.snapshot.Schemas[i].ID() != s.ID() {
			continue
		}
		for _, t := range p.snapshot.Schemas[i].Tables {
			delete(p.snapshot.Metadata, t.ID())
		}
		p.snapshot.Schemas[i].Tables = cloneSchemaValue(tables)
		p.snapshot.Schemas[i].Loaded = true
		p.snapshot.Schemas[i].FetchedAt = time.Now().UTC()
	}
	p.save()
	return cloneSchemaValue(tables), nil
}

func (p *Introspector) Table(ctx context.Context, t Table, force bool) (TableMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return TableMetadata{}, err
	}
	if m, ok := p.snapshot.Metadata[t.ID()]; ok && !force && fresh(m.FetchedAt) {
		m = cloneSchemaValue(m)
		m.FromCache = true
		return m, nil
	}
	m, err := p.source.TableMetadata(ctx, t)
	if err != nil {
		return TableMetadata{}, err
	}
	if m.FetchedAt.IsZero() {
		m.FetchedAt = time.Now().UTC()
	}
	if len(p.snapshot.Metadata) >= maxCachedTables {
		p.evictOldest()
	}
	p.snapshot.Metadata[t.ID()] = cloneSchemaValue(m)
	for i := range p.snapshot.Schemas {
		for j := range p.snapshot.Schemas[i].Tables {
			table := &p.snapshot.Schemas[i].Tables[j]
			if table.ID() == t.ID() {
				table.Kind = m.Kind
				table.Partition = cloneSchemaValue(m.Partition)
				table.Clustering = append([]string(nil), m.Clustering...)
			}
		}
	}
	p.save()
	return cloneSchemaValue(m), nil
}

func (p *Introspector) Section(ctx context.Context, t Table, name string, force bool) (TableMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return TableMetadata{}, err
	}
	m, ok := p.snapshot.Metadata[t.ID()]
	if !ok || !fresh(m.FetchedAt) {
		var err error
		m, err = p.source.TableMetadata(ctx, t)
		if err != nil {
			return TableMetadata{}, err
		}
	}
	m = cloneSchemaValue(m)
	state := m.Sections[name]
	if state.State == "unsupported" || !force && state.State == "loaded" && fresh(state.FetchedAt) {
		m.FromCache = true
		return m, nil
	}
	reader, ok := p.source.(interface {
		TableSection(context.Context, Table, string) ([]Definition, error)
	})
	if !ok {
		return m, fmt.Errorf("table section reader unavailable")
	}
	items, err := reader.TableSection(ctx, t, name)
	if err != nil {
		return m, err
	}
	if err := ctx.Err(); err != nil {
		return m, err
	}
	if m.FetchedAt.IsZero() {
		m.FetchedAt = time.Now().UTC()
	}
	m.setSectionItems(name, items)
	m.section(name, "loaded", "")
	if len(p.snapshot.Metadata) >= maxCachedTables {
		p.evictOldest()
	}
	p.snapshot.Metadata[t.ID()] = cloneSchemaValue(m)
	p.save()
	return cloneSchemaValue(m), nil
}

func (p *Introspector) evictOldest() {
	var key string
	var oldest time.Time
	for id, m := range p.snapshot.Metadata {
		if key == "" || m.FetchedAt.Before(oldest) {
			key, oldest = id, m.FetchedAt
		}
	}
	delete(p.snapshot.Metadata, key)
}

func (p *Introspector) read() {
	f, err := os.Open(p.path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		p.warning = "Schema cache unavailable: " + err.Error()
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCacheBytes+1))
	if err != nil || len(data) > maxCacheBytes {
		p.warning = "Schema cache unreadable or oversized; live discovery will rebuild it"
		return
	}
	var snapshot schemaSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != cacheVersion || snapshot.Key != p.snapshot.Key || len(snapshot.Schemas) > CatalogLimit || len(snapshot.Metadata) > maxCachedTables {
		p.warning = "Schema cache invalid or from an older version; live discovery will rebuild it"
		return
	}
	for _, s := range snapshot.Schemas {
		if len(s.Tables) > CatalogLimit {
			p.warning = "Schema cache exceeds object limits"
			return
		}
	}
	if snapshot.Metadata == nil {
		snapshot.Metadata = map[string]TableMetadata{}
	}
	for _, m := range snapshot.Metadata {
		count := 0
		if !validCachedColumns(m.Columns, 0, &count) {
			p.warning = "Schema cache has invalid nested fields; live discovery will rebuild it"
			return
		}
	}
	p.snapshot = snapshot
}

func validCachedColumns(columns []Column, depth int, count *int) bool {
	if depth > 32 {
		return false
	}
	for _, col := range columns {
		*count++
		if *count > CatalogLimit || !validCachedColumns(col.Fields, depth+1, count) {
			return false
		}
	}
	return true
}

func (p *Introspector) save() {
	if p.path == "" {
		return
	}
	snapshot := cloneSchemaValue(p.snapshot)
	if client, ok := p.source.(*Client); ok && client.Engine() == SQLite {
		// Temp and attached databases belong to the current session; persist only main.
		var schemas []Schema
		keep := map[string]bool{}
		for _, s := range snapshot.Schemas {
			if s.Name == "main" {
				schemas = append(schemas, s)
				for _, t := range s.Tables {
					keep[t.ID()] = true
				}
			}
		}
		snapshot.Schemas = schemas
		for id := range snapshot.Metadata {
			if !keep[id] {
				delete(snapshot.Metadata, id)
			}
		}
	}
	data, err := json.Marshal(snapshot)
	for err == nil && len(data) > maxCacheBytes && len(snapshot.Metadata) > 1 {
		var key string
		var oldest time.Time
		for id, m := range snapshot.Metadata {
			if key == "" || m.FetchedAt.Before(oldest) {
				key, oldest = id, m.FetchedAt
			}
		}
		delete(snapshot.Metadata, key)
		data, err = json.Marshal(snapshot)
	}
	if err == nil && len(data) > maxCacheBytes {
		err = fmt.Errorf("snapshot exceeds 32 MiB")
	}
	if err == nil {
		err = writeSchemaCache(p.path, data)
	}
	if err != nil {
		p.warning = "Schema cache could not be saved: " + err.Error()
	} else {
		p.warning = ""
	}
}

func writeSchemaCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "schema-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
