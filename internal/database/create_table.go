package database

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/stdlib"
)

type PostgreSQLColumn struct {
	Number                        int
	Comment, Using                string
	Name, Type, Default, Identity string
	NotNull, PrimaryKey, Unique   bool
}

type PostgreSQLConstraint struct {
	ID                                                          int64
	SQL, ReadOnlyReason                                         string
	Name, Kind, Columns, Expression                             string
	ReferenceSchema, ReferenceTable, ReferenceColumns, OnDelete string
}

type PostgreSQLTable struct {
	Indexes                                  []PostgreSQLIndex
	Name, Schema, Owner, Tablespace, Comment string
	Columns                                  []PostgreSQLColumn
	Constraints                              []PostgreSQLConstraint
	Unlogged, Partitioned                    bool
	PartitionMethod, PartitionColumns        string
	Fillfactor, Autovacuum                   string
	RowSecurity, ForceRowSecurity            bool
}

var pgType = regexp.MustCompile(`(?i)^(?:smallint|integer|int|int2|int4|int8|bigint|smallserial|serial|bigserial|real|double precision|boolean|bool|text|bytea|uuid|json|jsonb|date|money|inet|cidr|macaddr|xml|tsvector|tsquery|interval|(?:numeric|decimal|varchar|character varying|character|char|bit|bit varying)(?:\([0-9]+(?:\s*,\s*[0-9]+)?\))?|(?:timestamp|time)(?:\([0-9]+\))?(?: with(?:out)? time zone)?|timestamptz|timetz)(?:\[\])*$`)
var pgCustomType = regexp.MustCompile(`^(?:[a-zA-Z_][a-zA-Z_0-9]*|"(?:[^"]|"")+")(?:\.(?:[a-zA-Z_][a-zA-Z_0-9]*|"(?:[^"]|"")+"))?(?:\[\])*$`)

func pgIdentifier(name, label string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || len(name) > 63 {
		return fmt.Errorf("%s must be valid text, at most 63 bytes, without NUL", label)
	}
	return nil
}

func pgExpression(expr string) error {
	if strings.ContainsAny(expr, ";\x00") || strings.Contains(expr, "--") || strings.Contains(expr, "/*") {
		return fmt.Errorf("SQL expressions cannot contain statement separators or comments")
	}
	return nil
}

func pgColumnList(value string, columns map[string]bool) (string, []string, error) {
	var quoted, names []string
	seen := map[string]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if err := pgIdentifier(name, "Column name"); err != nil {
			return "", nil, err
		}
		if seen[name] {
			return "", nil, fmt.Errorf("column %q is repeated", name)
		}
		if columns != nil && !columns[name] {
			return "", nil, fmt.Errorf("column %q does not exist in this table", name)
		}
		seen[name] = true
		quoted, names = append(quoted, QuoteIdentifier(name)), append(names, name)
	}
	return strings.Join(quoted, ", "), names, nil
}

// Statements returns exactly the DDL used by Save and the SQL preview.
func (t PostgreSQLTable) Statements() ([]string, error) {
	if err := pgIdentifier(t.Name, "Table name"); err != nil {
		return nil, err
	}
	if err := pgIdentifier(t.Schema, "Schema"); err != nil {
		return nil, err
	}
	for label, value := range map[string]string{"Owner": t.Owner, "Tablespace": t.Tablespace} {
		if value != "" {
			if err := pgIdentifier(value, label); err != nil {
				return nil, err
			}
		}
	}
	if strings.ContainsRune(t.Comment, 0) {
		return nil, fmt.Errorf("comment cannot contain NUL")
	}
	if len(t.Columns) == 0 {
		return nil, fmt.Errorf("add at least one column")
	}
	if len(t.Columns) > 1600 {
		return nil, fmt.Errorf("a table can have at most 1600 columns")
	}
	columns := map[string]bool{}
	var definitions, primary []string
	var uniqueKeys [][]string
	for _, col := range t.Columns {
		if err := pgIdentifier(col.Name, "Column name"); err != nil {
			return nil, err
		}
		if columns[col.Name] {
			return nil, fmt.Errorf("duplicate column %q", col.Name)
		}
		columns[col.Name] = true
		if strings.ContainsRune(col.Type, 0) || !pgType.MatchString(col.Type) && !pgCustomType.MatchString(col.Type) {
			return nil, fmt.Errorf("invalid data type for column %q", col.Name)
		}
		if err := pgExpression(col.Default); err != nil {
			return nil, err
		}
		definition := QuoteIdentifier(col.Name) + " " + col.Type
		if col.Identity != "" && col.Identity != "None" {
			if col.Identity != "ALWAYS" && col.Identity != "BY DEFAULT" {
				return nil, fmt.Errorf("invalid identity mode")
			}
			switch strings.ToLower(col.Type) {
			case "smallint", "integer", "int", "bigint", "int2", "int4", "int8":
			default:
				return nil, fmt.Errorf("identity column %q must use an integer type", col.Name)
			}
			if strings.TrimSpace(col.Default) != "" {
				return nil, fmt.Errorf("identity column %q cannot also have a default", col.Name)
			}
			definition += " GENERATED " + col.Identity + " AS IDENTITY"
		} else if strings.TrimSpace(col.Default) != "" {
			definition += " DEFAULT (" + col.Default + ")"
		}
		if col.NotNull {
			definition += " NOT NULL"
		}
		if col.PrimaryKey {
			primary = append(primary, col.Name)
		}
		if col.Unique {
			definition += " UNIQUE"
			uniqueKeys = append(uniqueKeys, []string{col.Name})
		}
		definitions = append(definitions, definition)
	}
	constraintNames := map[string]bool{}
	for _, constraint := range t.Constraints {
		prefix := ""
		if constraint.Name != "" {
			if err := pgIdentifier(constraint.Name, "Constraint name"); err != nil {
				return nil, err
			}
			if constraintNames[constraint.Name] {
				return nil, fmt.Errorf("duplicate constraint name %q", constraint.Name)
			}
			constraintNames[constraint.Name] = true
			prefix = "CONSTRAINT " + QuoteIdentifier(constraint.Name) + " "
		}
		if constraint.Kind == "CHECK" {
			if strings.TrimSpace(constraint.Expression) == "" {
				return nil, fmt.Errorf("CHECK expression is required")
			}
			if err := pgExpression(constraint.Expression); err != nil {
				return nil, err
			}
			definitions = append(definitions, prefix+"CHECK ("+constraint.Expression+")")
			continue
		}
		list, names, err := pgColumnList(constraint.Columns, columns)
		if err != nil {
			return nil, err
		}
		switch constraint.Kind {
		case "PRIMARY KEY":
			if len(primary) > 0 {
				return nil, fmt.Errorf("define the primary key either in Columns or Constraints, not both")
			}
			primary = names
		case "UNIQUE":
			uniqueKeys = append(uniqueKeys, names)
		case "FOREIGN KEY":
			if err := pgIdentifier(constraint.ReferenceSchema, "Referenced schema"); err != nil {
				return nil, err
			}
			if err := pgIdentifier(constraint.ReferenceTable, "Referenced table"); err != nil {
				return nil, err
			}
			refs, refNames, err := pgColumnList(constraint.ReferenceColumns, nil)
			if err != nil {
				return nil, err
			}
			if len(refNames) != len(names) {
				return nil, fmt.Errorf("foreign key must have the same number of local and referenced columns")
			}
			definition := prefix + "FOREIGN KEY (" + list + ") REFERENCES " + QuoteIdentifier(constraint.ReferenceSchema) + "." + QuoteIdentifier(constraint.ReferenceTable) + " (" + refs + ")"
			if constraint.OnDelete != "" {
				switch constraint.OnDelete {
				case "NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT":
				default:
					return nil, fmt.Errorf("invalid ON DELETE action")
				}
				definition += " ON DELETE " + constraint.OnDelete
			}
			definitions = append(definitions, definition)
			continue
		default:
			return nil, fmt.Errorf("unsupported constraint kind %q", constraint.Kind)
		}
		definitions = append(definitions, prefix+constraint.Kind+" ("+list+")")
	}
	if len(primary) > 0 {
		uniqueKeys = append(uniqueKeys, primary)
		inline := false
		for _, constraint := range t.Constraints {
			if constraint.Kind == "PRIMARY KEY" {
				inline = true
			}
		}
		if !inline {
			var list []string
			for _, name := range primary {
				list = append(list, QuoteIdentifier(name))
			}
			definitions = append(definitions, "PRIMARY KEY ("+strings.Join(list, ", ")+")")
		}
	}
	ref := QuoteIdentifier(t.Schema) + "." + QuoteIdentifier(t.Name)
	create := "CREATE "
	if t.Unlogged {
		create += "UNLOGGED "
	}
	create += "TABLE " + ref + " (\n  " + strings.Join(definitions, ",\n  ") + "\n)"
	if t.Partitioned {
		if t.Unlogged {
			return nil, fmt.Errorf("unlogged partitioned tables are not supported by this dialog")
		}
		if t.PartitionMethod != "RANGE" && t.PartitionMethod != "LIST" && t.PartitionMethod != "HASH" {
			return nil, fmt.Errorf("choose a partition method")
		}
		list, keys, err := pgColumnList(t.PartitionColumns, columns)
		if err != nil {
			return nil, err
		}
		if len(keys) > 32 || t.PartitionMethod == "LIST" && len(keys) != 1 {
			return nil, fmt.Errorf("LIST partitioning requires one column; RANGE/HASH support up to 32")
		}
		for _, unique := range uniqueKeys {
			for _, key := range keys {
				found := false
				for _, col := range unique {
					if col == key {
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("every primary/unique key must include partition column %q", key)
				}
			}
		}
		create += " PARTITION BY " + t.PartitionMethod + " (" + list + ")"
	}
	var parameters []string
	if t.Fillfactor != "" {
		n, err := strconv.Atoi(t.Fillfactor)
		if err != nil || n < 10 || n > 100 {
			return nil, fmt.Errorf("fillfactor must be between 10 and 100")
		}
		parameters = append(parameters, "fillfactor = "+strconv.Itoa(n))
	}
	if t.Autovacuum != "" && t.Autovacuum != "Default" {
		if t.Autovacuum != "true" && t.Autovacuum != "false" {
			return nil, fmt.Errorf("invalid autovacuum setting")
		}
		parameters = append(parameters, "autovacuum_enabled = "+t.Autovacuum)
	}
	if len(parameters) > 0 {
		if t.Partitioned {
			return nil, fmt.Errorf("set storage parameters on individual partitions, not the partitioned parent")
		}
		create += " WITH (" + strings.Join(parameters, ", ") + ")"
	}
	if t.Tablespace != "" {
		create += " TABLESPACE " + QuoteIdentifier(t.Tablespace)
	}
	statements := []string{create}
	for _, col := range t.Columns {
		if strings.ContainsRune(col.Comment, 0) {
			return nil, fmt.Errorf("column comment cannot contain NUL")
		}
		if col.Comment != "" {
			statements = append(statements, "COMMENT ON COLUMN "+ref+"."+QuoteIdentifier(col.Name)+" IS "+pgLiteral(col.Comment))
		}
	}
	for _, index := range t.Indexes {
		statement, err := index.CreateSQL(Table{Schema: t.Schema, Name: t.Name}, columns)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	if t.Comment != "" {
		statements = append(statements, "COMMENT ON TABLE "+ref+" IS E'"+strings.ReplaceAll(strings.ReplaceAll(t.Comment, "\\", "\\\\"), "'", "''")+"'")
	}
	if t.RowSecurity {
		statements = append(statements, "ALTER TABLE "+ref+" ENABLE ROW LEVEL SECURITY")
	}
	if t.ForceRowSecurity {
		if !t.RowSecurity {
			return nil, fmt.Errorf("enable row security before forcing it")
		}
		statements = append(statements, "ALTER TABLE "+ref+" FORCE ROW LEVEL SECURITY")
	}
	// Change ownership last so all preceding steps execute as the creating role.
	if t.Owner != "" {
		statements = append(statements, "ALTER TABLE "+ref+" OWNER TO "+QuoteIdentifier(t.Owner))
	}
	return statements, nil
}

func (c *Client) CreatePostgreSQLTable(ctx context.Context, table PostgreSQLTable) error {
	if c.Engine() != PostgreSQL {
		return fmt.Errorf("table designer requires PostgreSQL")
	}
	statements, err := table.Statements()
	if err != nil {
		return err
	}
	conn := c.session
	if conn == nil {
		conn, err = c.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
	}
	err = conn.Raw(func(driver any) error {
		pg, ok := driver.(*stdlib.Conn)
		if !ok || pg.Conn().PgConn().TxStatus() != 'I' {
			return fmt.Errorf("finish the current transaction before creating a table")
		}
		return nil
	})
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return tx.Commit()
}
