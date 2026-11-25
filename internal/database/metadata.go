package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Column struct {
	Name, Type, Nullable, Default                                 string
	PrimaryKey, ForeignKey                                        bool
	Ordinal                                                       int
	NativeType, Mode, Description, Generated, Identity, Collation string
	Hidden, Partitioning                                          bool
	ClusteringOrdinal                                             int
	Fields                                                        []Column
	References                                                    []ColumnReference
	Properties                                                    []Property
}

type ColumnReference struct {
	Constraint, Project, Schema, Table, Column string
	Ordinal                                    int
}
type Definition struct {
	Name, Kind, SQL string
	Properties      []Property
}

type Property struct {
	Name, Value string
	Children    []Property
}
type MetadataSection struct {
	State, Message string
	FetchedAt      time.Time
}
type PartitionSpec struct {
	Strategy, Expression, Field, Parent, Bound string
	Columns                                    []string
	RangeStart, RangeEnd, RangeInterval        string
	ExpirationMillis                           int64
	IngestionTime, RequireFilter, IsPartition  bool
}

type TableMetadata struct {
	Columns                                 []Column
	RowEstimate, DDL, Note                  string
	Kind, Description                       string
	Partition                               PartitionSpec
	Clustering                              []string
	Constraints, Indexes, Triggers          []Definition
	Partitions, Policies, Privileges, Rules []Definition
	Properties                              []Property
	Sections                                map[string]MetadataSection
	FetchedAt                               time.Time
	FromCache                               bool `json:"-"`
}

func sqlLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (c *Client) TableMetadata(ctx context.Context, t Table) (out TableMetadata, err error) {
	if c.Engine() == BigQuery {
		return c.bigQueryMetadata(ctx, t)
	}
	if c.Engine() == MySQL {
		return c.mysqlMetadata(ctx, t)
	}
	defer func() {
		if err == nil {
			out.FetchedAt = time.Now().UTC()
			out.Kind = t.Kind
			out.Partition = t.Partition
			out.Clustering = t.Clustering
			err = c.enrichMetadata(ctx, t, &out)
			if err == nil {
				c.completeTableProperties(ctx, t, &out)
				err = ctx.Err()
			}
		}
	}()
	out.RowEstimate = "Unavailable"
	var cols Result
	switch c.Engine() {
	case SQLite:
		cols, err = c.catalogQuery(ctx, "SELECT name, type, [notnull], dflt_value, pk FROM pragma_table_xinfo("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") ORDER BY cid")
		if err != nil {
			return out, err
		}
		foreign, e := c.catalogQuery(ctx, "SELECT [from] FROM pragma_foreign_key_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+")")
		if e != nil {
			return out, e
		}
		keys := map[string]bool{}
		for _, r := range foreign.Rows {
			keys[r[0]] = true
		}
		for _, r := range cols.Rows {
			nullable := "YES"
			if r[2] == "1" || r[4] != "0" {
				nullable = "NO"
			}
			out.Columns = append(out.Columns, Column{Name: r[0], Type: r[1], Nullable: nullable, Default: r[3], PrimaryKey: r[4] != "0", ForeignKey: keys[r[0]]})
		}
		ddl, e := c.catalogQuery(ctx, "SELECT sql FROM "+QuoteIdentifier(t.Schema)+".sqlite_schema WHERE name="+sqlLiteral(t.Name)+" AND type IN ('table','view')")
		if e != nil {
			return out, e
		}
		if len(ddl.Rows) > 0 {
			out.DDL = ddl.Rows[0][0]
		}
		out.Note = "SQLite has no always-available row estimate; no COUNT(*) is run."
		stats, e := c.catalogQuery(ctx, "SELECT stat FROM "+QuoteIdentifier(t.Schema)+".sqlite_stat1 WHERE tbl="+sqlLiteral(t.Name)+" AND (idx IS NULL OR idx IN (SELECT name FROM pragma_index_list("+sqlLiteral(t.Name)+", "+sqlLiteral(t.Schema)+") WHERE partial=0)) LIMIT 1")
		if e == nil && len(stats.Rows) > 0 {
			if fields := strings.Fields(stats.Rows[0][0]); len(fields) > 0 {
				out.RowEstimate = "≈ " + fields[0]
				out.Note = "Planner statistics from ANALYZE; may be stale."
			}
		}
	case PostgreSQL:
		ref := sqlLiteral(QuoteIdentifier(t.Schema)+"."+QuoteIdentifier(t.Name)) + "::regclass"
		cols, err = c.catalogQuery(ctx, `SELECT a.attname, pg_catalog.format_type(a.atttypid,a.atttypmod), CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END,
 COALESCE(pg_get_expr(d.adbin,d.adrelid),''),
 EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conrelid=a.attrelid AND k.contype='p' AND a.attnum=ANY(k.conkey)),
 EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conrelid=a.attrelid AND k.contype='f' AND a.attnum=ANY(k.conkey))
 FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid=`+ref+` AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`)
		if err != nil {
			return out, err
		}
		for _, r := range cols.Rows {
			out.Columns = append(out.Columns, Column{Name: r[0], Type: r[1], Nullable: r[2], Default: r[3], PrimaryKey: r[4] == "true", ForeignKey: r[5] == "true"})
		}
		stats, e := c.catalogQuery(ctx, "SELECT reltuples::bigint, relkind FROM pg_class WHERE oid="+ref)
		if e != nil {
			return out, e
		}
		if len(stats.Rows) > 0 && stats.Rows[0][0] != "-1" {
			out.RowEstimate = "≈ " + stats.Rows[0][0]
		}
		if len(stats.Rows) > 0 && (stats.Rows[0][1] == "v" || stats.Rows[0][1] == "m") {
			def, e := c.catalogQuery(ctx, "SELECT pg_get_viewdef("+ref+",true)")
			if e != nil {
				return out, e
			}
			if len(def.Rows) > 0 {
				kind := "VIEW "
				if stats.Rows[0][1] == "m" {
					kind = "MATERIALIZED VIEW "
				}
				out.DDL = "CREATE " + kind + QuoteIdentifier(t.Schema) + "." + QuoteIdentifier(t.Name) + " AS\n" + def.Rows[0][0]
			}
		} else {
			var defs []string
			for _, col := range out.Columns {
				d := QuoteIdentifier(col.Name) + " " + col.Type
				if col.Default != "" {
					d += " DEFAULT " + col.Default
				}
				if col.Nullable == "NO" {
					d += " NOT NULL"
				}
				defs = append(defs, d)
			}
			constraints, e := c.catalogQuery(ctx, "SELECT pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conrelid="+ref+" ORDER BY conname")
			if e != nil {
				return out, e
			}
			for _, r := range constraints.Rows {
				defs = append(defs, r[0])
			}
			out.DDL = "-- Definition preview; indexes, triggers and storage options omitted.\nCREATE TABLE " + QuoteIdentifier(t.Schema) + "." + QuoteIdentifier(t.Name) + " (\n  " + strings.Join(defs, ",\n  ") + "\n);"
		}
	default:
		return out, fmt.Errorf("unsupported metadata engine")
	}
	if cols.Truncated {
		return out, fmt.Errorf("column metadata exceeds the display limit")
	}
	return out, nil
}
