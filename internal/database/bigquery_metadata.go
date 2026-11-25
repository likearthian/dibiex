package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	bqapi "google.golang.org/api/bigquery/v2"
)

func bigQueryKind(kind string, p PartitionSpec) string {
	switch kind {
	case "VIEW":
		return "Views"
	case "MATERIALIZED_VIEW":
		return "Materialized views"
	case "EXTERNAL":
		return "External tables"
	case "SNAPSHOT":
		return "Snapshots"
	case "CLONE":
		return "Clones"
	}
	if p.Strategy != "" {
		return "Partitioned tables"
	}
	return "Tables"
}

func bigQueryPartition(tp *bqapi.TimePartitioning, rp *bqapi.RangePartitioning, required bool) PartitionSpec {
	p := PartitionSpec{RequireFilter: required}
	if tp != nil {
		p.Strategy, p.Field, p.ExpirationMillis = tp.Type, tp.Field, tp.ExpirationMs
		if p.Strategy == "" {
			p.Strategy = "DAY"
		}
		p.RequireFilter = p.RequireFilter || tp.RequirePartitionFilter
		if tp.Field == "" {
			p.IngestionTime = true
			p.Field = "_PARTITIONTIME"
		}
		p.Columns = []string{p.Field}
	}
	if rp != nil {
		p.Strategy, p.Field = "RANGE", rp.Field
		p.Columns = []string{rp.Field}
		if rp.Range != nil {
			p.RangeStart = strconv.FormatInt(rp.Range.Start, 10)
			p.RangeEnd = strconv.FormatInt(rp.Range.End, 10)
			p.RangeInterval = strconv.FormatInt(rp.Range.Interval, 10)
		}
	}
	return p
}

func (c *Client) bigQueryMetadata(ctx context.Context, t Table) (TableMetadata, error) {
	resource, err := c.bqAPI.Tables.Get(t.Project, t.Schema, t.Name).Context(ctx).Do()
	if err != nil {
		return TableMetadata{}, fmt.Errorf("read BigQuery table metadata: %w", err)
	}
	out := TableMetadata{Description: resource.Description, FetchedAt: time.Now().UTC(), RowEstimate: "Unavailable"}
	for _, section := range []string{"columns", "constraints", "properties"} {
		out.section(section, "loaded", "")
	}
	for _, section := range []string{"indexes", "partitions"} {
		out.section(section, "unloaded", "Expand to query INFORMATION_SCHEMA using the connection billing cap")
	}
	for _, section := range []string{"policies", "privileges"} {
		out.section(section, "unloaded", "Expand to load from the BigQuery API")
	}
	for _, section := range []string{"triggers", "rules"} {
		out.section(section, "unsupported", "Not supported by BigQuery")
	}
	if props, e := propertiesOf(resource); e != nil {
		out.section("properties", "unavailable", e.Error())
	} else {
		out.Properties = []Property{{Name: "BigQuery table resource", Children: props}}
	}
	out.Partition = bigQueryPartition(resource.TimePartitioning, resource.RangePartitioning, resource.RequirePartitionFilter)
	if resource.PartitionDefinition != nil && out.Partition.Strategy == "" {
		out.Partition.Strategy = "EXTERNAL"
		for _, f := range resource.PartitionDefinition.PartitionedColumn {
			out.Partition.Columns = append(out.Partition.Columns, f.Field)
		}
	}
	out.Kind = bigQueryKind(resource.Type, out.Partition)
	if out.Partition.Strategy == "" {
		out.section("partitions", "loaded", "")
	}
	if resource.Clustering != nil {
		out.Clustering = append([]string(nil), resource.Clustering.Fields...)
	}
	if resource.Type != "VIEW" {
		out.RowEstimate = "≈ " + strconv.FormatUint(resource.NumRows, 10)
	}
	count := 0
	if resource.Schema != nil {
		out.Columns, err = bigQueryColumns(resource.Schema.Fields, 0, &count)
		if err != nil {
			return TableMetadata{}, err
		}
	}
	for i := range out.Columns {
		col := &out.Columns[i]
		for _, field := range out.Partition.Columns {
			if col.Name == field {
				col.Partitioning = true
			}
		}
		for n, field := range out.Clustering {
			if col.Name == field {
				col.ClusteringOrdinal = n + 1
			}
		}
	}
	if out.Partition.IngestionTime {
		out.Columns = append(out.Columns, Column{Name: "_PARTITIONTIME", Type: "TIMESTAMP", NativeType: "TIMESTAMP", Nullable: "YES", Mode: "PSEUDO", Hidden: true, Partitioning: true, Description: "Ingestion-time partition pseudocolumn"})
		if out.Partition.Strategy == "DAY" {
			out.Columns = append(out.Columns, Column{Name: "_PARTITIONDATE", Type: "DATE", NativeType: "DATE", Nullable: "YES", Mode: "PSEUDO", Hidden: true, Partitioning: true})
		}
	}
	if keys := resource.TableConstraints; keys != nil {
		if keys.PrimaryKey != nil {
			out.Constraints = append(out.Constraints, Definition{Kind: "PRIMARY KEY", SQL: strings.Join(keys.PrimaryKey.Columns, ", ") + " (not enforced)"})
			for i := range out.Columns {
				for _, name := range keys.PrimaryKey.Columns {
					if out.Columns[i].Name == name {
						out.Columns[i].PrimaryKey = true
					}
				}
			}
		}
		for _, fk := range keys.ForeignKeys {
			if fk == nil || fk.ReferencedTable == nil {
				continue
			}
			ref := fk.ReferencedTable
			var pairs []string
			for n, pair := range fk.ColumnReferences {
				if pair == nil {
					continue
				}
				pairs = append(pairs, pair.ReferencingColumn+" → "+pair.ReferencedColumn)
				for i := range out.Columns {
					if out.Columns[i].Name == pair.ReferencingColumn {
						out.Columns[i].ForeignKey = true
						out.Columns[i].References = append(out.Columns[i].References, ColumnReference{Constraint: fk.Name, Project: ref.ProjectId, Schema: ref.DatasetId, Table: ref.TableId, Column: pair.ReferencedColumn, Ordinal: n + 1})
					}
				}
			}
			out.Constraints = append(out.Constraints, Definition{Name: fk.Name, Kind: "FOREIGN KEY", SQL: strings.Join(pairs, ", ") + " → " + ref.ProjectId + "." + ref.DatasetId + "." + ref.TableId + " (not enforced)"})
		}
	}
	// REST supplies schema without query jobs. DDL below is a definition preview.
	if resource.View != nil {
		out.DDL = "CREATE VIEW " + t.Reference(BigQuery) + " AS\n" + resource.View.Query
	} else if resource.MaterializedView != nil {
		out.DDL = "CREATE MATERIALIZED VIEW " + t.Reference(BigQuery) + " AS\n" + resource.MaterializedView.Query
	} else {
		out.DDL = "-- Definition preview; external storage and table options omitted.\nCREATE TABLE " + t.Reference(BigQuery) + " (\n" + bigQueryColumnDDL(out.Columns, "  ") + "\n)"
		p := out.Partition
		if p.Strategy == "RANGE" {
			out.DDL += "\nPARTITION BY RANGE_BUCKET(" + bqIdentifier(p.Field) + ", GENERATE_ARRAY(" + p.RangeStart + ", " + p.RangeEnd + ", " + p.RangeInterval + "))"
		} else if p.Strategy != "" && p.Strategy != "EXTERNAL" {
			field := bqIdentifier(p.Field)
			if p.IngestionTime {
				field = "_PARTITIONTIME"
			}
			out.DDL += "\nPARTITION BY " + field
			out.Note = "Partition specification is shown separately; DDL is a preview."
		}
		if len(out.Clustering) > 0 {
			names := make([]string, len(out.Clustering))
			for i, n := range out.Clustering {
				names[i] = bqIdentifier(n)
			}
			out.DDL += "\nCLUSTER BY " + strings.Join(names, ", ")
		}
		out.DDL += ";"
	}
	out.Note += " Metadata from the table API; row estimates exclude streaming buffers."
	return out, nil
}

func bqIdentifier(name string) string {
	return "`" + strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(name) + "`"
}

func bigQueryColumns(fields []*bqapi.TableFieldSchema, depth int, count *int) ([]Column, error) {
	if depth > 32 {
		return nil, fmt.Errorf("nested schema exceeds 32 levels")
	}
	var columns []Column
	for i, f := range fields {
		if f == nil {
			continue
		}
		*count++
		if *count > CatalogLimit {
			return nil, fmt.Errorf("nested schema exceeds %d fields", CatalogLimit)
		}
		typ := strings.ToUpper(f.Type)
		switch typ {
		case "INTEGER":
			typ = "INT64"
		case "FLOAT":
			typ = "FLOAT64"
		case "BOOLEAN":
			typ = "BOOL"
		case "STRUCT":
			typ = "RECORD"
		}
		mode := f.Mode
		if mode == "" {
			mode = "NULLABLE"
		}
		col := Column{Name: f.Name, Type: typ, NativeType: f.Type, Mode: mode, Ordinal: i + 1, Nullable: "YES", Description: f.Description, Default: f.DefaultValueExpression, Collation: f.Collation}
		copy := *f
		copy.Fields = nil
		props, err := propertiesOf(&copy)
		if err != nil {
			return nil, err
		}
		col.Properties = props
		if f.GeneratedColumn != nil && f.GeneratedColumn.GeneratedExpressionInfo != nil {
			col.Generated = f.GeneratedColumn.GeneratedExpressionInfo.GenerationExpression
		}
		if mode == "REQUIRED" {
			col.Nullable = "NO"
		}
		if mode == "REPEATED" {
			col.Type = "ARRAY<" + typ + ">"
			col.Nullable = "NO"
		}
		col.Fields, err = bigQueryColumns(f.Fields, depth+1, count)
		if err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}
	return columns, nil
}

func bigQueryColumnDDL(columns []Column, indent string) string {
	var defs []string
	for _, col := range columns {
		if col.Hidden {
			continue
		}
		typ := col.Type
		if len(col.Fields) > 0 {
			typ = "STRUCT<\n" + bigQueryColumnDDL(col.Fields, indent+"  ") + "\n" + indent + ">"
			if col.Mode == "REPEATED" {
				typ = "ARRAY<" + typ + ">"
			}
		}
		d := indent + bqIdentifier(col.Name) + " " + typ
		if col.Mode == "REQUIRED" {
			d += " NOT NULL"
		}
		if col.Default != "" {
			d += " DEFAULT " + col.Default
		}
		defs = append(defs, d)
	}
	return strings.Join(defs, ",\n")
}
