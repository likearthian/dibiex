package database

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func (c *Client) completeTableProperties(ctx context.Context, t Table, m *TableMetadata) {
	for _, section := range []string{"columns", "indexes", "partitions", "constraints", "triggers", "policies", "privileges", "rules", "properties"} {
		m.section(section, "loaded", "")
	}
	if c.Engine() == SQLite {
		c.sqliteTableProperties(ctx, t, m)
	} else {
		c.postgresTableProperties(ctx, t, m)
	}
	var columns []Property
	for _, col := range m.Columns {
		columns = append(columns, Property{Name: col.Name, Children: col.Properties})
	}
	m.Properties = append(m.Properties, Property{Name: "Columns", Children: columns})
}

func (c *Client) postgresTableProperties(ctx context.Context, t Table, m *TableMetadata) {
	ref := sqlLiteral(QuoteIdentifier(t.Schema)+"."+QuoteIdentifier(t.Name)) + "::regclass"
	props, err := c.catalogQuery(ctx, `SELECT to_jsonb(c)::text, pg_get_userbyid(c.relowner), COALESCE(obj_description(c.oid,'pg_class'),''),
 COALESCE(ts.spcname,''), COALESCE(am.amname,''), CASE WHEN c.relkind IN ('r','m','p') THEN pg_total_relation_size(c.oid) ELSE 0 END
 FROM pg_class c LEFT JOIN pg_tablespace ts ON ts.oid=c.reltablespace LEFT JOIN pg_am am ON am.oid=c.relam WHERE c.oid=`+ref)
	if err != nil {
		m.section("properties", "unavailable", err.Error())
	} else if len(props.Rows) > 0 {
		r := props.Rows[0]
		raw, e := propertiesJSON([]byte(r[0]))
		if e != nil {
			m.section("properties", "unavailable", e.Error())
		} else {
			m.Properties = []Property{property("Owner", r[1]), property("Comment", r[2]), property("Tablespace", r[3]), property("Access method", r[4]), property("Own relation storage bytes", r[5]), {Name: "Native pg_class", Children: raw}}
			m.Description = r[2]
		}
	}
	columns, e := c.catalogQuery(ctx, "SELECT attname,to_jsonb(a)::text FROM pg_attribute a WHERE attrelid="+ref+" AND attnum>0 AND NOT attisdropped ORDER BY attnum")
	if e != nil {
		m.section("columns", "partial", e.Error())
	} else {
		for _, r := range columns.Rows {
			for i := range m.Columns {
				if m.Columns[i].Name == r[0] {
					col := &m.Columns[i]
					native, err := propertiesJSON([]byte(r[1]))
					if err != nil {
						m.section("columns", "partial", err.Error())
						continue
					}
					col.Properties = []Property{property("Default / generated expression", col.Default), property("Identity", col.Identity), property("Generated", col.Generated), property("Description", col.Description), property("Collation", col.Collation), {Name: "Native pg_attribute", Children: native}}
				}
			}
		}
	}
	queries := []struct {
		section, sql string
		target       *[]Definition
	}{
		{"constraints", "SELECT conname,contype::text,pg_get_constraintdef(oid,true),to_jsonb(k)::text FROM pg_constraint k WHERE conrelid=" + ref + " ORDER BY conname", &m.Constraints},
		{"indexes", `SELECT cl.relname,'INDEX',pg_get_indexdef(i.indexrelid),
 (to_jsonb(i)||jsonb_build_object('access_method',am.amname,'index_relation',to_jsonb(cl)))::text
 FROM pg_index i JOIN pg_class cl ON cl.oid=i.indexrelid JOIN pg_am am ON am.oid=cl.relam WHERE i.indrelid=` + ref + " ORDER BY cl.relname", &m.Indexes},
		{"partitions", `SELECT n.nspname||'.'||cl.relname,'PARTITION',COALESCE(pg_get_expr(cl.relpartbound,cl.oid),''),
 (to_jsonb(cl)||jsonb_build_object('partition_key',pg_get_partkeydef(cl.oid),'schema',n.nspname))::text
 FROM pg_inherits i JOIN pg_class cl ON cl.oid=i.inhrelid JOIN pg_namespace n ON n.oid=cl.relnamespace
 WHERE i.inhparent=` + ref + " AND cl.relispartition ORDER BY n.nspname,cl.relname", &m.Partitions},
		{"triggers", "SELECT tgname,'TRIGGER',pg_get_triggerdef(oid,true),to_jsonb(t)::text FROM pg_trigger t WHERE tgrelid=" + ref + " AND NOT tgisinternal ORDER BY tgname", &m.Triggers},
		{"policies", "SELECT policyname,'POLICY',COALESCE(qual,'')||' WITH CHECK '||COALESCE(with_check,''),to_jsonb(p)::text FROM pg_policies p WHERE schemaname=" + sqlLiteral(t.Schema) + " AND tablename=" + sqlLiteral(t.Name) + " ORDER BY policyname", &m.Policies},
		{"rules", "SELECT rulename,'RULE',pg_get_ruledef(oid,true),to_jsonb(r)::text FROM pg_rewrite r WHERE ev_class=" + ref + " AND rulename<>'_RETURN' ORDER BY rulename", &m.Rules},
		{"privileges", `SELECT CASE WHEN x.grantee=0 THEN 'PUBLIC' ELSE pg_get_userbyid(x.grantee) END||' · '||x.privilege_type,'GRANT',
 CASE WHEN x.is_grantable THEN 'WITH GRANT OPTION' ELSE '' END,to_jsonb(x)::text
 FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) x WHERE c.oid=` + ref + " ORDER BY 1", &m.Privileges},
	}
	for _, q := range queries {
		rows, e := c.catalogQuery(ctx, q.sql)
		if e != nil {
			m.section(q.section, "unavailable", e.Error())
			continue
		}
		var defs []Definition
		for _, r := range rows.Rows {
			p, e := propertiesJSON([]byte(r[3]))
			if e != nil {
				m.section(q.section, "partial", e.Error())
			}
			defs = append(defs, Definition{Name: r[0], Kind: r[1], SQL: r[2], Properties: p})
		}
		*q.target = defs
	}
	inherit, e := c.catalogQuery(ctx, `SELECT parent.oid::regclass::text,child.oid::regclass::text,i.inhseqno FROM pg_inherits i
 JOIN pg_class parent ON parent.oid=i.inhparent JOIN pg_class child ON child.oid=i.inhrelid
 WHERE i.inhparent=`+ref+" OR i.inhrelid="+ref+" ORDER BY 1,2")
	if e == nil {
		m.Properties = append(m.Properties, Property{Name: "Inheritance", Children: resultProperties(inherit)})
	} else {
		m.section("properties", "partial", "Inheritance: "+e.Error())
	}
	if t.Kind == "Foreign tables" {
		rows, e := c.catalogQuery(ctx, `SELECT (to_jsonb(ft)||jsonb_build_object('server',s.srvname,'server_options',s.srvoptions,'wrapper',w.fdwname))::text
 FROM pg_foreign_table ft JOIN pg_foreign_server s ON s.oid=ft.ftserver JOIN pg_foreign_data_wrapper w ON w.oid=s.srvfdw WHERE ft.ftrelid=`+ref)
		if e != nil {
			m.section("properties", "partial", e.Error())
		} else if len(rows.Rows) > 0 {
			p, err := propertiesJSON([]byte(rows.Rows[0][0]))
			if err != nil {
				m.section("properties", "partial", err.Error())
			} else {
				m.Properties = append(m.Properties, Property{Name: "Foreign table", Children: p})
			}
		}
	}
	partition, e := c.catalogQuery(ctx, "SELECT to_jsonb(p)::text FROM pg_partitioned_table p WHERE partrelid="+ref)
	if e != nil {
		m.section("properties", "partial", "Partition catalog: "+e.Error())
	} else if len(partition.Rows) > 0 {
		p, err := propertiesJSON([]byte(partition.Rows[0][0]))
		if err != nil {
			m.section("properties", "partial", err.Error())
		} else {
			m.Properties = append(m.Properties, Property{Name: "Native partition specification", Children: p})
		}
	}
}

func resultProperties(rows Result) []Property {
	var out []Property
	for i, row := range rows.Rows {
		p := Property{Name: strconv.Itoa(i + 1)}
		for j, value := range row {
			if j < len(rows.Columns) {
				p.Children = append(p.Children, Property{Name: rows.Columns[j], Value: redactPropertyString(value)})
			}
		}
		out = append(out, p)
	}
	return out
}

func (c *Client) sqliteTableProperties(ctx context.Context, t Table, m *TableMetadata) {
	m.section("partitions", "unsupported", "SQLite has no native table partitioning")
	for _, key := range []string{"policies", "privileges", "rules"} {
		m.section(key, "unsupported", "Not exposed as table properties by SQLite")
	}
	flags, err := c.catalogQuery(ctx, "SELECT schema,name,type,ncol,wr,strict FROM pragma_table_list WHERE schema="+sqlLiteral(t.Schema)+" AND name="+sqlLiteral(t.Name))
	if err != nil {
		m.section("properties", "unavailable", err.Error())
	} else {
		m.Properties = []Property{{Name: "Native table properties", Children: resultProperties(flags)}, property("Source DDL", m.DDL)}
	}
	columns, err := c.catalogQuery(ctx, "SELECT cid,name,type,[notnull],dflt_value,pk,hidden FROM pragma_table_xinfo("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY cid")
	if err != nil {
		m.section("columns", "partial", err.Error())
		return
	}
	pk := map[int]string{}
	var constraints []Definition
	for _, r := range columns.Rows {
		for i := range m.Columns {
			if m.Columns[i].Name == r[1] {
				rows := Result{Columns: columns.Columns, Rows: [][]string{r}}
				m.Columns[i].Properties = resultProperties(rows)[0].Children
			}
		}
		if ordinal, _ := strconv.Atoi(r[5]); ordinal > 0 {
			pk[ordinal] = r[1]
		}
		if r[3] == "1" {
			constraints = append(constraints, Definition{Name: r[1] + " NOT NULL", Kind: "NOT NULL", SQL: QuoteIdentifier(r[1]) + " NOT NULL"})
		}
	}
	if len(pk) > 0 {
		positions := make([]int, 0, len(pk))
		for n := range pk {
			positions = append(positions, n)
		}
		sort.Ints(positions)
		var names []string
		for _, n := range positions {
			names = append(names, QuoteIdentifier(pk[n]))
		}
		constraints = append(constraints, Definition{Name: "Primary key", Kind: "PRIMARY KEY", SQL: "PRIMARY KEY (" + strings.Join(names, ", ") + ")"})
	}
	foreign := map[string]*Definition{}
	var order []string
	for _, d := range m.Constraints {
		if d.Kind != "FOREIGN KEY" {
			continue
		}
		if old := foreign[d.Name]; old != nil {
			old.SQL += "; " + d.SQL
		} else {
			copy := d
			foreign[d.Name] = &copy
			order = append(order, d.Name)
		}
	}
	for _, name := range order {
		constraints = append(constraints, *foreign[name])
	}
	foreignRows, e := c.catalogQuery(ctx, "SELECT id,seq,[table],[from],[to],on_update,on_delete,[match] FROM pragma_foreign_key_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY id,seq")
	if e != nil {
		m.section("constraints", "partial", e.Error())
	} else {
		for i := range constraints {
			if constraints[i].Kind == "FOREIGN KEY" {
				var rows [][]string
				for _, r := range foreignRows.Rows {
					if constraints[i].Name == "fk_"+r[0] {
						rows = append(rows, r)
					}
				}
				constraints[i].Properties = resultProperties(Result{Columns: foreignRows.Columns, Rows: rows})
			}
		}
	}
	indexes, e := c.catalogQuery(ctx, "SELECT seq,name,[unique],origin,partial FROM pragma_index_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY seq")
	if e != nil {
		m.section("indexes", "unavailable", e.Error())
	} else {
		var defs []Definition
		for _, r := range indexes.Rows {
			d := Definition{Name: r[1], Kind: "INDEX"}
			for _, old := range m.Indexes {
				if old.Name == d.Name {
					d.SQL = old.SQL
				}
			}
			d.Properties = []Property{property("Unique", r[2] == "1"), property("Origin", r[3]), property("Partial", r[4] == "1")}
			terms, e := c.catalogQuery(ctx, "SELECT seqno,cid,name,[desc],coll,[key] FROM pragma_index_xinfo("+sqlLiteral(d.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY seqno")
			if e != nil {
				m.section("indexes", "partial", e.Error())
			} else {
				d.Properties = append(d.Properties, Property{Name: "Columns / expressions", Children: resultProperties(terms)})
			}
			defs = append(defs, d)
			if r[2] == "1" && r[3] == "u" {
				constraints = append(constraints, Definition{Name: d.Name, Kind: "UNIQUE", SQL: d.SQL, Properties: d.Properties})
			}
		}
		m.Indexes = defs
	}
	constraints = append(constraints, sqliteCheckConstraints(m.DDL)...)
	m.Constraints = constraints
	for i := range m.Triggers {
		m.Triggers[i].Properties = []Property{property("Source SQL", m.Triggers[i].SQL)}
	}
}

type sqlMetadataToken struct {
	text, kind string
	start, end int
}

func sqliteCheckConstraints(sql string) []Definition {
	tokens := sqliteMetadataTokens(sql)
	var out []Definition
	for i, t := range tokens {
		if t.kind != "word" || !strings.EqualFold(t.text, "CHECK") || i+1 >= len(tokens) || tokens[i+1].text != "(" {
			continue
		}
		depth := 0
		end := -1
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].kind != "punct" {
				continue
			}
			if tokens[j].text == "(" {
				depth++
			}
			if tokens[j].text == ")" {
				depth--
				if depth == 0 {
					end = tokens[j].end
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		name := fmt.Sprintf("Check %d", len(out)+1)
		if i >= 2 && strings.EqualFold(tokens[i-2].text, "CONSTRAINT") {
			name = tokens[i-1].text
		}
		out = append(out, Definition{Name: name, Kind: "CHECK", SQL: sql[t.start:end]})
	}
	return out
}

func sqliteMetadataTokens(sql string) []sqlMetadataToken {
	var out []sqlMetadataToken
	for i := 0; i < len(sql); {
		start := i
		b := sql[i]
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			i++
			continue
		}
		if strings.HasPrefix(sql[i:], "--") {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(sql[i:], "/*") {
			i += 2
			for i < len(sql) && !strings.HasPrefix(sql[i:], "*/") {
				i++
			}
			i = min(i+2, len(sql))
			continue
		}
		if b == '\'' || b == '"' || b == '`' || b == '[' {
			close := b
			if b == '[' {
				close = ']'
			}
			i++
			var value strings.Builder
			for i < len(sql) {
				if sql[i] == close {
					if close != ']' && i+1 < len(sql) && sql[i+1] == close {
						value.WriteByte(close)
						i += 2
						continue
					}
					i++
					break
				}
				value.WriteByte(sql[i])
				i++
			}
			kind := "identifier"
			if b == '\'' {
				kind = "string"
			}
			out = append(out, sqlMetadataToken{value.String(), kind, start, i})
			continue
		}
		if b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' {
			i++
			for i < len(sql) && (sql[i] == '_' || sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9') {
				i++
			}
			out = append(out, sqlMetadataToken{sql[start:i], "word", start, i})
			continue
		}
		i++
		out = append(out, sqlMetadataToken{sql[start:i], "punct", start, i})
	}
	return out
}
