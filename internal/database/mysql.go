package database

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// mysqlDSN builds the driver connection string. The driver keeps parsing and
// escaping in one place; dibiex never logs the assembled DSN because it holds
// the password.
func (cfg Config) mysqlDSN() string {
	conf := mysql.NewConfig()
	conf.User = cfg.User
	conf.Passwd = cfg.Password
	conf.Net = "tcp"
	conf.Addr = net.JoinHostPort(cfg.Host, cfg.Port)
	conf.DBName = cfg.Database
	conf.TLSConfig = cfg.SSLMode
	// A SQL console replays pasted scripts like the other engines do.
	conf.MultiStatements = true
	conf.Params = map[string]string{"charset": "utf8mb4"}
	return conf.FormatDSN()
}

// mysqlBinaryType reports column types whose bytes are not human-readable text.
// go-sql-driver returns every value as []byte unless parseTime is enabled.
func mysqlBinaryType(name string) bool {
	switch strings.ToUpper(name) {
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "GEOMETRY", "BIT":
		return true
	}
	return false
}

// driverCellValue keeps textual cells readable across drivers while preserving
// real binary presentation. pgx and go-sql-driver both return text as []byte.
func driverCellValue(engine Engine, value any, typeName string) any {
	data, ok := value.([]byte)
	if !ok {
		return value
	}
	switch engine {
	case PostgreSQL:
		if typeName == "JSON" || typeName == "JSONB" {
			return string(data)
		}
	case MySQL:
		if !mysqlBinaryType(typeName) {
			return string(data)
		}
	}
	return value
}

func (c *Client) mysqlCatalogNames(ctx context.Context) ([]string, error) {
	rows, err := c.catalogQuery(ctx, `SELECT schema_name FROM information_schema.schemata
 WHERE schema_name NOT IN ('mysql','information_schema','performance_schema','sys')
 ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	if rows.Truncated {
		return nil, fmt.Errorf("catalog exceeds the 10,000 schema display limit")
	}
	names := make([]string, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		names = append(names, row[0])
	}
	return names, nil
}

type mysqlIndex struct {
	name, kind string
	unique     bool
	columns    []string
	properties []Property
}

func mysqlIndexSQL(index mysqlIndex) string {
	quoted := make([]string, len(index.columns))
	for i, column := range index.columns {
		quoted[i] = QuoteIdentifierFor(MySQL, column)
	}
	columns := strings.Join(quoted, ", ")
	switch {
	case index.name == "PRIMARY":
		return "PRIMARY KEY (" + columns + ")"
	case index.unique:
		return "CREATE UNIQUE INDEX " + QuoteIdentifierFor(MySQL, index.name) + " (" + columns + ")"
	default:
		return "CREATE INDEX " + QuoteIdentifierFor(MySQL, index.name) + " (" + columns + ")"
	}
}

type mysqlConstraint struct {
	name, kind, check string
	columns           []string
	references        []string
	referenceSchema   string
	referenceTable    string
}

func mysqlConstraintSQL(constraint mysqlConstraint) string {
	quoted := make([]string, len(constraint.columns))
	for i, column := range constraint.columns {
		quoted[i] = QuoteIdentifierFor(MySQL, column)
	}
	columns := strings.Join(quoted, ", ")
	switch constraint.kind {
	case "PRIMARY KEY":
		return "PRIMARY KEY (" + columns + ")"
	case "UNIQUE":
		return "UNIQUE (" + columns + ")"
	case "FOREIGN KEY":
		refs := make([]string, len(constraint.references))
		for i, column := range constraint.references {
			refs[i] = QuoteIdentifierFor(MySQL, column)
		}
		ref := QuoteIdentifierFor(MySQL, constraint.referenceTable)
		if constraint.referenceSchema != "" {
			ref = QuoteIdentifierFor(MySQL, constraint.referenceSchema) + "." + ref
		}
		return "FOREIGN KEY (" + columns + ") REFERENCES " + ref + " (" + strings.Join(refs, ", ") + ")"
	case "CHECK":
		if constraint.check != "" {
			return "CHECK " + constraint.check
		}
		return "CHECK"
	}
	return constraint.kind
}

// mysqlLiteral escapes a value for MySQL string literals. Backslashes are
// always escaped because MySQL interprets them inside literals by default.
func mysqlLiteral(value string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", "''").Replace(value) + "'"
}

func mysqlPartitionSQL(method, description string) string {
	switch strings.ToUpper(method) {
	case "RANGE", "RANGE COLUMNS":
		if description != "" {
			return "VALUES LESS THAN (" + description + ")"
		}
	case "LIST", "LIST COLUMNS":
		if description != "" {
			return "VALUES IN (" + description + ")"
		}
	}
	if description != "" {
		return description
	}
	return method
}

// mysqlMetadata assembles table details from information_schema plus the exact
// SHOW CREATE TABLE definition. It mirrors the PostgreSQL detail contract.
func (c *Client) mysqlMetadata(ctx context.Context, t Table) (TableMetadata, error) {
	out := TableMetadata{
		FetchedAt:   time.Now().UTC(),
		Kind:        t.Kind,
		Partition:   t.Partition,
		Clustering:  t.Clustering,
		RowEstimate: "Unavailable",
	}
	where := "table_schema=" + mysqlLiteral(t.Schema) + " AND table_name=" + mysqlLiteral(t.Name)

	info, err := c.catalogQuery(ctx, `SELECT COALESCE(engine,''), COALESCE(row_format,''), COALESCE(table_collation,''),
 COALESCE(table_comment,''), COALESCE(create_options,''), COALESCE(auto_increment,0),
 COALESCE(table_rows,0), COALESCE(data_length,0), COALESCE(index_length,0)
 FROM information_schema.tables WHERE `+where)
	if err != nil {
		return out, err
	}
	if len(info.Rows) == 0 {
		return out, fmt.Errorf("table metadata is unavailable; refresh the catalog")
	}
	if info.Truncated {
		return out, fmt.Errorf("table metadata exceeds the display limit")
	}
	row := info.Rows[0]
	out.Description = row[3]
	estimated := row[6]
	if row[0] == "" {
		estimated = "Unavailable"
	}
	out.Properties = []Property{
		property("Engine", row[0]),
		property("Row format", row[1]),
		property("Collation", row[2]),
		property("Comment", row[3]),
		property("Create options", row[4]),
		property("Auto increment", row[5]),
		property("Estimated rows", estimated),
		property("Data length bytes", row[7]),
		property("Index length bytes", row[8]),
	}
	if row[0] != "" {
		out.RowEstimate = "≈ " + row[6]
		out.Note = "Information schema row estimates; InnoDB statistics may be stale. No COUNT(*) is run."
	} else {
		out.Note = "Views have no row estimate."
	}

	cols, err := c.catalogQuery(ctx, `SELECT column_name, column_type, data_type, is_nullable,
 COALESCE(column_default,''), COALESCE(column_key,''), COALESCE(extra,''),
 COALESCE(collation_name,''), COALESCE(column_comment,''), COALESCE(generation_expression,'')
 FROM information_schema.columns WHERE `+where+` ORDER BY ordinal_position`)
	if err != nil {
		return out, err
	}
	if cols.Truncated {
		return out, fmt.Errorf("column metadata exceeds the display limit")
	}
	foreign, err := c.catalogQuery(ctx, `SELECT column_name, constraint_name, referenced_table_schema,
 referenced_table_name, referenced_column_name, ordinal_position
 FROM information_schema.key_column_usage
 WHERE `+where+` AND referenced_table_name IS NOT NULL
 ORDER BY constraint_name, ordinal_position`)
	if err != nil {
		return out, err
	}
	references := map[string][]ColumnReference{}
	for _, r := range foreign.Rows {
		ordinal := 0
		_, _ = fmt.Sscan(r[5], &ordinal)
		references[r[0]] = append(references[r[0]], ColumnReference{Constraint: r[1], Schema: r[2], Table: r[3], Column: r[4], Ordinal: ordinal})
	}
	for _, r := range cols.Rows {
		col := Column{
			Name:        r[0],
			Type:        r[1],
			NativeType:  r[2],
			Nullable:    r[3],
			Default:     r[4],
			PrimaryKey:  r[5] == "PRI",
			ForeignKey:  len(references[r[0]]) > 0,
			Collation:   r[7],
			Description: r[8],
			References:  references[r[0]],
		}
		extra := strings.ToLower(r[6])
		if strings.Contains(extra, "auto_increment") {
			col.Identity = "AUTO_INCREMENT"
		}
		if strings.Contains(extra, "virtual generated") {
			col.Generated = "VIRTUAL"
		} else if strings.Contains(extra, "stored generated") {
			col.Generated = "STORED"
		}
		col.Properties = []Property{
			property("Default", col.Default),
			property("Extra", r[6]),
			property("Generated", col.Generated),
			property("Collation", col.Collation),
			property("Comment", col.Description),
			property("Native data type", col.NativeType),
		}
		out.Columns = append(out.Columns, col)
	}
	for i := range out.Columns {
		out.Columns[i].Ordinal = i + 1
	}

	indexes, err := c.catalogQuery(ctx, `SELECT index_name, non_unique, index_type, column_name, seq_in_index
 FROM information_schema.statistics WHERE `+where+` ORDER BY index_name, seq_in_index`)
	if err != nil {
		return out, err
	}
	var order []string
	byIndex := map[string]*mysqlIndex{}
	for _, r := range indexes.Rows {
		index, ok := byIndex[r[0]]
		if !ok {
			index = &mysqlIndex{name: r[0], unique: r[1] == "0"}
			switch {
			case r[0] == "PRIMARY":
				index.kind = "PRIMARY KEY"
			case index.unique:
				index.kind = "UNIQUE"
			default:
				index.kind = "INDEX"
			}
			index.properties = []Property{property("Access method", r[2]), property("Unique", r[1] == "0")}
			byIndex[r[0]] = index
			order = append(order, r[0])
		}
		if r[3] != "" {
			index.columns = append(index.columns, r[3])
		}
	}
	for _, name := range order {
		index := byIndex[name]
		index.properties = append(index.properties, Property{Name: "Columns", Children: propertyNames(index.columns)})
		out.Indexes = append(out.Indexes, Definition{Name: index.name, Kind: index.kind, SQL: mysqlIndexSQL(*index), Properties: index.properties})
	}

	checks := map[string]string{}
	if checkRows, checkErr := c.catalogQuery(ctx, `SELECT cc.constraint_name, cc.check_clause
 FROM information_schema.check_constraints cc
 JOIN information_schema.table_constraints tc
  ON tc.constraint_schema=cc.constraint_schema AND tc.constraint_name=cc.constraint_name
 WHERE tc.table_schema=`+mysqlLiteral(t.Schema)+` AND tc.table_name=`+mysqlLiteral(t.Name)); checkErr == nil {
		for _, r := range checkRows.Rows {
			checks[r[0]] = r[1]
		}
	}

	constraints, err := c.catalogQuery(ctx, `SELECT tc.constraint_name, tc.constraint_type, COALESCE(kcu.column_name,''),
 COALESCE(kcu.referenced_table_schema,''), COALESCE(kcu.referenced_table_name,''), COALESCE(kcu.referenced_column_name,'')
 FROM information_schema.table_constraints tc
 LEFT JOIN information_schema.key_column_usage kcu
  ON kcu.constraint_schema=tc.constraint_schema AND kcu.constraint_name=tc.constraint_name
  AND kcu.table_schema=tc.table_schema AND kcu.table_name=tc.table_name
 WHERE tc.table_schema=`+mysqlLiteral(t.Schema)+` AND tc.table_name=`+mysqlLiteral(t.Name)+`
 ORDER BY tc.constraint_name, kcu.ordinal_position`)
	if err != nil {
		return out, err
	}
	var constraintOrder []string
	byConstraint := map[string]*mysqlConstraint{}
	for _, r := range constraints.Rows {
		constraint, ok := byConstraint[r[0]]
		if !ok {
			constraint = &mysqlConstraint{name: r[0], kind: r[1], check: checks[r[0]]}
			byConstraint[r[0]] = constraint
			constraintOrder = append(constraintOrder, r[0])
		}
		if r[2] != "" {
			constraint.columns = append(constraint.columns, r[2])
		}
		if r[4] != "" {
			constraint.referenceSchema, constraint.referenceTable = r[3], r[4]
			constraint.references = append(constraint.references, r[5])
		}
	}
	for _, name := range constraintOrder {
		constraint := byConstraint[name]
		properties := []Property{{Name: "Columns", Children: propertyNames(constraint.columns)}}
		if constraint.referenceTable != "" {
			properties = append(properties, property("References", constraint.referenceSchema+"."+constraint.referenceTable))
		}
		out.Constraints = append(out.Constraints, Definition{Name: constraint.name, Kind: constraint.kind, SQL: mysqlConstraintSQL(*constraint), Properties: properties})
	}

	triggers, err := c.catalogQuery(ctx, `SELECT trigger_name, action_timing, event_manipulation, action_statement
 FROM information_schema.triggers WHERE event_object_schema=`+mysqlLiteral(t.Schema)+` AND event_object_table=`+mysqlLiteral(t.Name)+`
 ORDER BY trigger_name`)
	if err != nil {
		return out, err
	}
	for _, r := range triggers.Rows {
		out.Triggers = append(out.Triggers, Definition{Name: r[0], Kind: "TRIGGER", SQL: r[1] + " " + r[2] + " " + r[3]})
	}

	partitions, err := c.catalogQuery(ctx, `SELECT partition_name, COALESCE(partition_method,''), COALESCE(partition_expression,''),
 COALESCE(partition_description,''), COALESCE(partition_ordinal_position,0)
 FROM information_schema.partitions WHERE `+where+` AND partition_name IS NOT NULL
 ORDER BY partition_ordinal_position`)
	if err != nil {
		return out, err
	}
	for _, r := range partitions.Rows {
		if out.Partition.Strategy == "" {
			out.Partition.Strategy, out.Partition.Expression = r[1], r[2]
		}
		out.Partitions = append(out.Partitions, Definition{Name: r[0], Kind: "PARTITION", SQL: mysqlPartitionSQL(r[1], r[3]), Properties: []Property{property("Method", r[1]), property("Expression", r[2]), property("Bound", r[3])}})
	}

	ddl, err := c.catalogQuery(ctx, "SHOW CREATE TABLE "+t.Reference(MySQL))
	if err != nil {
		return out, err
	}
	if len(ddl.Rows) > 0 && len(ddl.Rows[0]) > 1 {
		out.DDL = ddl.Rows[0][1]
	}

	for _, section := range []string{"columns", "indexes", "constraints", "triggers", "properties"} {
		out.section(section, "loaded", "")
	}
	if len(out.Partitions) == 0 {
		out.section("partitions", "unsupported", "Table is not partitioned")
	} else {
		out.section("partitions", "loaded", "")
	}
	out.section("policies", "unsupported", "MySQL does not expose row-level security policies in information_schema")
	out.section("privileges", "unsupported", "Table grants are not loaded for MySQL connections")
	out.section("rules", "unsupported", "MySQL has no rewrite rules")

	var columns []Property
	for _, col := range out.Columns {
		columns = append(columns, Property{Name: col.Name, Children: col.Properties})
	}
	out.Properties = append(out.Properties, Property{Name: "Columns", Children: columns})
	return out, nil
}

func propertyNames(names []string) []Property {
	out := make([]Property, len(names))
	for i, name := range names {
		out[i] = Property{Name: name}
	}
	return out
}
