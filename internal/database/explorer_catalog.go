package database

import (
	"context"
	"fmt"
	"time"
)

type ExplorerGroup struct {
	Scope, Name, State, Message string
	Items                       []Definition
}
type ExplorerCatalog struct {
	Version   int
	Engine    Engine
	Database  string
	Groups    []ExplorerGroup
	FetchedAt time.Time
}

// ExplorerObjects supplements table discovery with PostgreSQL's real catalogs.
// It performs bounded reads and belongs on a worker, never in a render callback.
func (c *Client) ExplorerObjects(ctx context.Context) (ExplorerCatalog, error) {
	out := ExplorerCatalog{Version: 1, Engine: c.Engine(), Database: c.Scope(), FetchedAt: time.Now().UTC()}
	if c.Engine() != PostgreSQL {
		return out, nil
	}
	schemaFilter := ` n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%' `
	queries := []struct{ scope, name, sql string }{
		{"schema", "routines", `SELECT n.nspname,p.proname||'('||pg_get_function_identity_arguments(p.oid)||')',pg_get_functiondef(p.oid),to_jsonb(p)::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.prokind<>'a' AND ` + schemaFilter + ` ORDER BY 1,2`},
		{"schema", "aggregates", `SELECT n.nspname,p.proname||'('||pg_get_function_identity_arguments(p.oid)||')','',(to_jsonb(p)||jsonb_build_object('aggregate',to_jsonb(a)))::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_aggregate a ON a.aggfnoid=p.oid WHERE ` + schemaFilter + ` ORDER BY 1,2`},
		{"schema", "sequences", `SELECT n.nspname,c.relname,'',(to_jsonb(c)||jsonb_build_object('sequence',to_jsonb(s),'data_type',format_type(s.seqtypid,NULL),'owner',pg_get_userbyid(c.relowner),'schema',n.nspname))::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_sequence s ON s.seqrelid=c.oid WHERE ` + schemaFilter + ` ORDER BY 1,2`},
		{"schema", "object types", `SELECT n.nspname,t.typname,'',(to_jsonb(t)||jsonb_build_object('enum_labels',(SELECT jsonb_agg(e.enumlabel ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid=t.oid)))::text FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.typelem=0 AND (t.typrelid=0 OR EXISTS(SELECT 1 FROM pg_class c WHERE c.oid=t.typrelid AND c.relkind='c')) AND ` + schemaFilter + ` ORDER BY 1,2`},
		{"database", "access methods", `SELECT '',amname,'',to_jsonb(a)::text FROM pg_am a ORDER BY amname`},
		{"database", "casts", `SELECT '',format_type(castsource,NULL)||' → '||format_type(casttarget,NULL),'',to_jsonb(c)::text FROM pg_cast c ORDER BY 2`},
		{"database", "extensions", `SELECT '',extname,'',to_jsonb(e)::text FROM pg_extension e ORDER BY extname`},
		{"database", "languages", `SELECT '',lanname,'',to_jsonb(l)::text FROM pg_language l ORDER BY lanname`},
		{"server", "roles", `SELECT '',rolname,'',(to_jsonb(r)-'rolpassword')::text FROM pg_roles r ORDER BY rolname`},
		{"server", "tablespaces", `SELECT '',spcname,'',(to_jsonb(t)||jsonb_build_object('location',pg_tablespace_location(oid)))::text FROM pg_tablespace t ORDER BY spcname`},
	}
	objects, bytes := 0, 0
	for _, q := range queries {
		rows, err := c.catalogQuery(ctx, q.sql)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Groups = append(out.Groups, ExplorerGroup{Scope: q.scope, Name: q.name, State: "unavailable", Message: err.Error()})
			continue
		}
		groups := map[string]int{}
		for _, r := range rows.Rows {
			objects++
			for _, value := range r {
				bytes += len(value)
			}
			if objects > CatalogLimit || bytes > 32<<20 {
				return out, fmt.Errorf("extended catalog exceeds the bounded metadata limit")
			}
			scope := q.scope
			if scope == "schema" {
				scope = "schema:" + r[0]
			}
			index, ok := groups[scope]
			if !ok {
				index = len(out.Groups)
				groups[scope] = index
				out.Groups = append(out.Groups, ExplorerGroup{Scope: scope, Name: q.name, State: "loaded"})
			}
			props, e := propertiesJSON([]byte(r[3]))
			if e != nil {
				out.Groups[index].State = "partial"
				out.Groups[index].Message = e.Error()
			}
			out.Groups[index].Items = append(out.Groups[index].Items, Definition{Name: r[1], Kind: q.name, SQL: r[2], Properties: props})
		}
		if q.scope != "schema" && len(rows.Rows) == 0 {
			out.Groups = append(out.Groups, ExplorerGroup{Scope: q.scope, Name: q.name, State: "loaded"})
		}
	}
	out.Groups = append(out.Groups, ExplorerGroup{Scope: "virtual", Name: "virtual views", State: "loaded", Items: []Definition{{Name: "sessions", Kind: "VIRTUAL VIEW", SQL: `SELECT pid,usename,application_name,client_addr,state,wait_event_type,wait_event,query_start,query FROM pg_catalog.pg_stat_activity WHERE datname=current_database() ORDER BY pid;`, Properties: []Property{{Name: "Source", Value: "pg_catalog.pg_stat_activity"}, {Name: "Scope", Value: "Current database; visibility depends on role permissions"}}}}})
	return out, ctx.Err()
}

func (p *Introspector) Explorer(ctx context.Context, force bool) (ExplorerCatalog, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ExplorerCatalog{}, err
	}
	if !force && p.snapshot.Explorer != nil && p.snapshot.Explorer.Version == 1 && fresh(p.snapshot.Explorer.FetchedAt) {
		return cloneSchemaValue(*p.snapshot.Explorer), nil
	}
	source, ok := p.source.(interface {
		ExplorerObjects(context.Context) (ExplorerCatalog, error)
	})
	if !ok {
		return ExplorerCatalog{}, fmt.Errorf("extended catalog unavailable")
	}
	out, err := source.ExplorerObjects(ctx)
	if err != nil {
		return ExplorerCatalog{}, err
	}
	p.snapshot.Explorer = &out
	p.save()
	return cloneSchemaValue(out), nil
}
