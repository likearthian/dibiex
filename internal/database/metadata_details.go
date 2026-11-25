package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (c *Client) enrichMetadata(ctx context.Context, t Table, m *TableMetadata) error {
	for i := range m.Columns {
		m.Columns[i].Ordinal = i + 1
		m.Columns[i].NativeType = m.Columns[i].Type
	}
	if c.Engine() == SQLite {
		return c.sqliteDetails(ctx, t, m)
	}
	ref := sqlLiteral(QuoteIdentifier(t.Schema)+"."+QuoteIdentifier(t.Name)) + "::regclass"
	cols, err := c.catalogQuery(ctx, `SELECT a.attname, a.attidentity::text, a.attgenerated::text,
 COALESCE(col_description(a.attrelid,a.attnum),''), COALESCE(coll.collname,''),
 EXISTS(SELECT 1 FROM pg_partitioned_table p WHERE p.partrelid=a.attrelid AND a.attnum=ANY(p.partattrs)), a.attnum
 FROM pg_attribute a LEFT JOIN pg_collation coll ON coll.oid=a.attcollation
 WHERE a.attrelid=`+ref+` AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`)
	if err != nil {
		return err
	}
	for _, r := range cols.Rows {
		for i := range m.Columns {
			if m.Columns[i].Name == r[0] {
				col := &m.Columns[i]
				col.Identity = r[1]
				col.Generated = r[2]
				if col.Identity == "a" {
					col.Identity = "ALWAYS"
				}
				if col.Identity == "d" {
					col.Identity = "BY DEFAULT"
				}
				if col.Generated == "s" {
					col.Generated = "STORED"
				}
				if col.Generated == "v" {
					col.Generated = "VIRTUAL"
				}
				col.Ordinal, _ = strconv.Atoi(r[6])
				col.Description = r[3]
				col.Collation = r[4]
				col.Partitioning = r[5] == "true"
			}
		}
	}
	constraints, err := c.catalogQuery(ctx, "SELECT conname, contype::text, pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conrelid="+ref+" ORDER BY conname")
	if err != nil {
		return err
	}
	for _, r := range constraints.Rows {
		m.Constraints = append(m.Constraints, Definition{Name: r[0], Kind: r[1], SQL: r[2]})
	}
	refs, err := c.catalogQuery(ctx, `SELECT a.attname, n.nspname, cl.relname, ra.attname, k.conname, u.ord
 FROM pg_constraint k CROSS JOIN LATERAL unnest(k.conkey,k.confkey) WITH ORDINALITY u(attnum,refnum,ord)
 JOIN pg_attribute a ON a.attrelid=k.conrelid AND a.attnum=u.attnum
 JOIN pg_class cl ON cl.oid=k.confrelid JOIN pg_namespace n ON n.oid=cl.relnamespace
 JOIN pg_attribute ra ON ra.attrelid=k.confrelid AND ra.attnum=u.refnum
 WHERE k.contype='f' AND k.conrelid=`+ref+` ORDER BY k.conname,u.ord`)
	if err != nil {
		return err
	}
	for _, r := range refs.Rows {
		for i := range m.Columns {
			if m.Columns[i].Name == r[0] {
				n, _ := strconv.Atoi(r[5])
				m.Columns[i].References = append(m.Columns[i].References, ColumnReference{Constraint: r[4], Schema: r[1], Table: r[2], Column: r[3], Ordinal: n})
			}
		}
	}
	indexes, err := c.catalogQuery(ctx, "SELECT indexname, indexdef FROM pg_indexes WHERE schemaname="+sqlLiteral(t.Schema)+" AND tablename="+sqlLiteral(t.Name)+" ORDER BY indexname")
	if err != nil {
		return err
	}
	for _, r := range indexes.Rows {
		m.Indexes = append(m.Indexes, Definition{Name: r[0], Kind: "INDEX", SQL: r[1]})
	}
	triggers, err := c.catalogQuery(ctx, "SELECT tgname,pg_get_triggerdef(oid,true) FROM pg_trigger WHERE tgrelid="+ref+" AND NOT tgisinternal ORDER BY tgname")
	if err != nil {
		return err
	}
	for _, r := range triggers.Rows {
		m.Triggers = append(m.Triggers, Definition{Name: r[0], Kind: "TRIGGER", SQL: r[1]})
	}
	// Fetch fresh specs even when an older cached table identity was expanded.
	parts, err := c.catalogQuery(ctx, `SELECT COALESCE(pg_get_partkeydef(c.oid),''),
 COALESCE((SELECT inhparent::regclass::text FROM pg_inherits WHERE inhrelid=c.oid ORDER BY inhseqno LIMIT 1),''),
 COALESCE(pg_get_expr(c.relpartbound,c.oid),''), c.relispartition, COALESCE(p.partstrat::text,'')
 FROM pg_class c LEFT JOIN pg_partitioned_table p ON p.partrelid=c.oid WHERE c.oid=`+ref)
	if err != nil {
		return err
	}
	if len(parts.Rows) > 0 {
		r := parts.Rows[0]
		m.Partition = PartitionSpec{Expression: r[0], Parent: r[1], Bound: r[2], IsPartition: r[3] == "true", Strategy: r[4]}
		if r[0] != "" {
			m.Kind = "Partitioned tables"
		}
		for _, col := range m.Columns {
			if col.Partitioning {
				m.Partition.Columns = append(m.Partition.Columns, col.Name)
			}
		}
	}
	if t.Kind != "Views" && t.Kind != "Materialized views" {
		m.DDL = postgresDefinition(t, *m)
	}
	return nil
}

func postgresDefinition(t Table, m TableMetadata) string {
	var defs []string
	for _, col := range m.Columns {
		d := QuoteIdentifier(col.Name) + " " + col.Type
		if col.Generated != "" {
			d += " GENERATED ALWAYS AS (" + col.Default + ") " + col.Generated
		} else if col.Identity != "" {
			d += " GENERATED " + col.Identity + " AS IDENTITY"
		} else if col.Default != "" {
			d += " DEFAULT " + col.Default
		}
		if col.Nullable == "NO" {
			d += " NOT NULL"
		}
		defs = append(defs, d)
	}
	for _, c := range m.Constraints {
		defs = append(defs, "CONSTRAINT "+QuoteIdentifier(c.Name)+" "+c.SQL)
	}
	sql := "-- Definition preview; indexes, triggers, inheritance and storage options omitted.\nCREATE TABLE " + t.Reference(PostgreSQL) + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"
	if m.Partition.Expression != "" {
		sql += " PARTITION BY " + m.Partition.Expression
	}
	return sql + ";"
}

func (c *Client) sqliteDetails(ctx context.Context, t Table, m *TableMetadata) error {
	cols, err := c.catalogQuery(ctx, "SELECT cid,name,hidden,[notnull],pk FROM pragma_table_xinfo("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY cid")
	if err != nil {
		return err
	}
	flags, err := c.catalogQuery(ctx, "SELECT wr,strict FROM pragma_table_list WHERE schema="+sqlLiteral(t.Schema)+" AND name="+sqlLiteral(t.Name))
	if err != nil {
		return err
	}
	strictPK := len(flags.Rows) > 0 && (flags.Rows[0][0] == "1" || flags.Rows[0][1] == "1")
	pkIndex, err := c.catalogQuery(ctx, "SELECT name FROM pragma_index_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") WHERE origin='pk' LIMIT 1")
	if err != nil {
		return err
	}
	pkCount := 0
	for _, col := range m.Columns {
		if col.PrimaryKey {
			pkCount++
		}
	}
	for _, r := range cols.Rows {
		for i := range m.Columns {
			if m.Columns[i].Name == r[1] {
				col := &m.Columns[i]
				cid, _ := strconv.Atoi(r[0])
				col.Ordinal = cid + 1
				col.Hidden = r[2] == "1"
				if r[2] == "2" {
					col.Generated = "VIRTUAL"
				}
				if r[2] == "3" {
					col.Generated = "STORED"
				}
				col.Nullable = "YES"
				if r[3] == "1" || (col.PrimaryKey && (strictPK || (pkCount == 1 && strings.EqualFold(col.Type, "INTEGER") && len(pkIndex.Rows) == 0))) {
					col.Nullable = "NO"
				}
			}
		}
	}
	fks, err := c.catalogQuery(ctx, "SELECT id,seq,[table],[from],[to],on_update,on_delete FROM pragma_foreign_key_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY id,seq")
	if err != nil {
		return err
	}
	parentKeys := map[string][]string{}
	for _, r := range fks.Rows {
		name := "fk_" + r[0]
		n, _ := strconv.Atoi(r[1])
		refCol := r[4]
		if refCol == "NULL" {
			keys, ok := parentKeys[r[2]]
			if !ok {
				rows, e := c.catalogQuery(ctx, "SELECT name FROM pragma_table_info("+sqlLiteral(r[2])+", "+sqlLiteral(t.Schema)+") WHERE pk>0 ORDER BY pk")
				if e != nil {
					return e
				}
				for _, row := range rows.Rows {
					keys = append(keys, row[0])
				}
				parentKeys[r[2]] = keys
			}
			refCol = "(primary key)"
			if n < len(keys) {
				refCol = keys[n]
			}
		}
		for i := range m.Columns {
			if m.Columns[i].Name == r[3] {
				m.Columns[i].References = append(m.Columns[i].References, ColumnReference{Constraint: name, Schema: t.Schema, Table: r[2], Column: refCol, Ordinal: n + 1})
			}
		}
		m.Constraints = append(m.Constraints, Definition{Name: name, Kind: "FOREIGN KEY", SQL: r[3] + " → " + r[2] + "." + refCol + " ON UPDATE " + r[5] + " ON DELETE " + r[6]})
	}
	defs, err := c.catalogQuery(ctx, "SELECT type,name,COALESCE(sql,'') FROM "+QuoteIdentifier(t.Schema)+".sqlite_schema WHERE tbl_name="+sqlLiteral(t.Name)+" AND type IN ('index','trigger') ORDER BY type,name")
	if err != nil {
		return err
	}
	for _, r := range defs.Rows {
		d := Definition{Name: r[1], Kind: strings.ToUpper(r[0]), SQL: r[2]}
		if r[0] == "index" {
			m.Indexes = append(m.Indexes, d)
		} else {
			m.Triggers = append(m.Triggers, d)
		}
	}
	for _, col := range m.Columns {
		if col.PrimaryKey {
			m.Constraints = append(m.Constraints, Definition{Kind: "PRIMARY KEY", SQL: col.Name})
		}
	}
	m.Note += " SQLite does not support native table partitioning; generated expressions remain in the source DDL."
	return nil
}

func (p PartitionSpec) Summary() string {
	if p.Strategy == "" {
		if p.IsPartition {
			return "Partition of " + p.Parent + " · " + p.Bound
		}
		return "Not partitioned"
	}
	if p.Expression != "" {
		return p.Expression
	}
	if p.Strategy == "RANGE" {
		return fmt.Sprintf("RANGE %s · [%s, %s) every %s", p.Field, p.RangeStart, p.RangeEnd, p.RangeInterval)
	}
	return p.Strategy + " · " + strings.Join(p.Columns, ", ")
}
