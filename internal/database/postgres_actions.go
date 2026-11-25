package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

type TableAction struct {
	ObjectID                                                                                                                   int64
	Kind, Name, Type, Columns, Expression, ReferenceSchema, ReferenceTable, ReferenceColumns, Function, Event, Timing, Command string
	Cascade, RestartIdentity, Unique, NotNull                                                                                  bool
	RowSecurity, ForceRowSecurity                                                                                              bool
}

func (a TableAction) SQL(t Table) (string, error) {
	if err := pgIdentifier(t.Schema, "Schema"); err != nil {
		return "", err
	}
	if err := pgIdentifier(t.Name, "Table"); err != nil {
		return "", err
	}
	ref := t.Reference(PostgreSQL)
	if a.Kind == "Properties" {
		statements, err := a.propertyStatements(t)
		if err != nil {
			return "", err
		}
		return "BEGIN;\n" + strings.Join(statements, ";\n") + ";\nCOMMIT;", nil
	}
	for _, value := range []string{a.Expression} {
		if err := pgExpression(value); err != nil {
			return "", err
		}
	}
	switch a.Kind {
	case "Drop":
		if a.Cascade {
			return "DROP TABLE " + ref + " CASCADE;", nil
		}
		return "DROP TABLE " + ref + ";", nil
	case "Truncate":
		s := "TRUNCATE TABLE " + ref
		if a.RestartIdentity {
			s += " RESTART IDENTITY"
		}
		if a.Cascade {
			s += " CASCADE"
		}
		return s + ";", nil
	case "Reset Statistics":
		return "SELECT pg_stat_reset_single_table_counters(" + pgLiteral(ref) + "::regclass);", nil
	case "Maintenance":
		switch a.Command {
		case "VACUUM", "VACUUM (ANALYZE)", "VACUUM (FULL, ANALYZE)", "ANALYZE", "REINDEX TABLE":
			return a.Command + " " + ref + ";", nil
		}
		return "", fmt.Errorf("choose a maintenance operation")
	}
	if err := pgIdentifier(a.Name, "Object name"); err != nil {
		return "", err
	}
	name := QuoteIdentifier(a.Name)
	switch a.Kind {
	case "Column":
		if !pgType.MatchString(a.Type) && !pgCustomType.MatchString(a.Type) {
			return "", fmt.Errorf("enter a valid PostgreSQL data type")
		}
		s := "ALTER TABLE " + ref + " ADD COLUMN " + name + " " + a.Type
		if a.Expression != "" {
			s += " DEFAULT (" + a.Expression + ")"
		}
		if a.NotNull {
			s += " NOT NULL"
		}
		return s + ";", nil
	case "Index":
		list, _, err := pgColumnList(a.Columns, nil)
		if err != nil {
			return "", err
		}
		prefix := "CREATE "
		if a.Unique {
			prefix += "UNIQUE "
		}
		return prefix + "INDEX " + name + " ON " + ref + " (" + list + ");", nil
	case "Primary key", "Unique", "Check", "Foreign key":
		prefix := "ALTER TABLE " + ref + " ADD CONSTRAINT " + name + " "
		if a.Kind == "Check" {
			if strings.TrimSpace(a.Expression) == "" {
				return "", fmt.Errorf("enter a check expression")
			}
			return prefix + "CHECK (" + a.Expression + ");", nil
		}
		list, names, err := pgColumnList(a.Columns, nil)
		if err != nil {
			return "", err
		}
		if a.Kind == "Primary key" {
			return prefix + "PRIMARY KEY (" + list + ");", nil
		}
		if a.Kind == "Unique" {
			return prefix + "UNIQUE (" + list + ");", nil
		}
		if err := pgIdentifier(a.ReferenceSchema, "Referenced schema"); err != nil {
			return "", err
		}
		if err := pgIdentifier(a.ReferenceTable, "Referenced table"); err != nil {
			return "", err
		}
		refs, refNames, err := pgColumnList(a.ReferenceColumns, nil)
		if err != nil {
			return "", err
		}
		if len(names) != len(refNames) {
			return "", fmt.Errorf("local and referenced column counts must match")
		}
		return prefix + "FOREIGN KEY (" + list + ") REFERENCES " + QuoteIdentifier(a.ReferenceSchema) + "." + QuoteIdentifier(a.ReferenceTable) + " (" + refs + ");", nil
	case "Trigger":
		if a.Timing != "BEFORE" && a.Timing != "AFTER" {
			return "", fmt.Errorf("choose BEFORE or AFTER")
		}
		if a.Event != "INSERT" && a.Event != "UPDATE" && a.Event != "DELETE" {
			return "", fmt.Errorf("choose a trigger event")
		}
		if err := pgIdentifier(a.ReferenceSchema, "Function schema"); err != nil {
			return "", err
		}
		if err := pgIdentifier(a.Function, "Trigger function"); err != nil {
			return "", err
		}
		return "CREATE TRIGGER " + name + " " + a.Timing + " " + a.Event + " ON " + ref + " FOR EACH ROW EXECUTE FUNCTION " + QuoteIdentifier(a.ReferenceSchema) + "." + QuoteIdentifier(a.Function) + "();", nil
	case "Rule":
		if a.Event != "INSERT" && a.Event != "UPDATE" && a.Event != "DELETE" {
			return "", fmt.Errorf("choose a rule event")
		}
		if strings.TrimSpace(a.Expression) == "" {
			return "", fmt.Errorf("enter a rule SQL command")
		}
		if strings.EqualFold(strings.TrimSpace(a.Expression), "NOTHING") {
			return "CREATE RULE " + name + " AS ON " + a.Event + " TO " + ref + " DO ALSO NOTHING;", nil
		}
		return "CREATE RULE " + name + " AS ON " + a.Event + " TO " + ref + " DO ALSO (" + a.Expression + ");", nil
	case "Policy":
		if strings.TrimSpace(a.Expression) == "" {
			return "", fmt.Errorf("enter a row-security expression")
		}
		return "CREATE POLICY " + name + " ON " + ref + " USING (" + a.Expression + ") WITH CHECK (" + a.Expression + ");", nil
	}
	return "", fmt.Errorf("unsupported table action")
}

func pgLiteral(value string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "''") + "'"
}

func (c *Client) PostgreSQLColumns(ctx context.Context, t Table) ([]Column, error) {
	result, err := c.catalogQuery(ctx, `SELECT a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull::text,a.attidentity::text,a.attgenerated::text,EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conrelid=a.attrelid AND k.contype='p' AND a.attnum=ANY(k.conkey))::text FROM pg_attribute a WHERE a.attrelid=`+pgLiteral(t.Reference(PostgreSQL))+`::regclass AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`)
	if err != nil {
		return nil, err
	}
	var cols []Column
	for i, row := range result.Rows {
		cols = append(cols, Column{Name: row[0], Type: row[1], Nullable: map[bool]string{true: "NO", false: "YES"}[row[2] == "true" || row[2] == "t"], Identity: row[3], Generated: row[4], PrimaryKey: row[5] == "true" || row[5] == "t", Ordinal: i + 1})
	}
	return cols, nil
}

type TableCell struct {
	Text          string
	Null, Include bool
}
type TableData struct {
	Columns   []Column
	Rows      [][]TableCell
	Truncated bool
}

func (c *Client) PostgreSQLProperties(ctx context.Context, t Table) (TableAction, error) {
	if c.Engine() != PostgreSQL {
		return TableAction{}, fmt.Errorf("table editing requires PostgreSQL")
	}
	if c.session != nil {
		return readPostgreSQLTableProperties(ctx, c.session, t)
	}
	return readPostgreSQLTableProperties(ctx, c.db, t)
}

func (c *Client) ReadPostgreSQLTable(ctx context.Context, t Table, limit int, filter string, last bool) (TableData, error) {
	return c.ReadPostgreSQLTablePage(ctx, t, limit, 0, filter, last)
}

func (c *Client) ReadPostgreSQLTablePage(ctx context.Context, t Table, limit, offset int, filter string, last bool) (TableData, error) {
	var data TableData
	if c.Engine() != PostgreSQL {
		return data, fmt.Errorf("data editor requires PostgreSQL")
	}
	if err := pgExpression(filter); err != nil {
		return data, err
	}
	limit = max(1, min(limit, MaxRows))
	cols, err := c.PostgreSQLColumns(ctx, t)
	if err != nil {
		return data, err
	}
	data.Columns = cols
	var selections, keys []string
	for _, col := range cols {
		selections = append(selections, QuoteIdentifier(col.Name)+"::text")
		if col.PrimaryKey {
			key := QuoteIdentifier(col.Name)
			if last {
				key += " DESC"
			}
			keys = append(keys, key)
		}
	}
	query := "SELECT " + strings.Join(selections, ", ") + " FROM " + t.Reference(PostgreSQL)
	if strings.TrimSpace(filter) != "" {
		query += " WHERE (" + filter + ")"
	}
	if len(keys) > 0 {
		query += " ORDER BY " + strings.Join(keys, ", ")
	} else if last {
		return data, fmt.Errorf("last rows requires a primary key for deterministic ordering")
	}
	query += " LIMIT " + strconv.Itoa(limit+1) + " OFFSET " + strconv.Itoa(max(0, min(offset, 1000000000)))
	rows, err := c.session.QueryContext(ctx, query)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		if len(data.Rows) == limit {
			data.Truncated = true
			break
		}
		values := make([]sql.NullString, len(cols))
		args := make([]any, len(cols))
		for i := range values {
			args[i] = &values[i]
		}
		if err := rows.Scan(args...); err != nil {
			return data, err
		}
		row := make([]TableCell, len(cols))
		for i, value := range values {
			if len(value.String) > maxCellBytes {
				return data, fmt.Errorf("cell exceeds 4 KiB; use Query Tool to inspect this table without editing")
			}
			total += len(value.String)
			row[i] = TableCell{Text: value.String, Null: !value.Valid, Include: true}
		}
		if total > maxResultBytes {
			data.Truncated = true
			break
		}
		data.Rows = append(data.Rows, row)
	}
	return data, rows.Err()
}

func (c *Client) ChangePostgreSQLRow(ctx context.Context, t Table, cols []Column, original, values []TableCell, action string) error {
	if c.Engine() != PostgreSQL {
		return fmt.Errorf("data editing requires PostgreSQL")
	}
	var args []any
	var setters, names, params, predicates []string
	if action != "Insert" && len(original) != len(cols) {
		return fmt.Errorf("select a row")
	}
	if action != "Delete" && len(values) != len(cols) {
		return fmt.Errorf("invalid row values")
	}
	for i, col := range cols {
		if action != "Delete" && col.Generated == "" && col.Identity == "" && values[i].Include {
			var value any
			if !values[i].Null {
				value = values[i].Text
			}
			args = append(args, value)
			param := "$" + strconv.Itoa(len(args))
			setters = append(setters, QuoteIdentifier(col.Name)+" = "+param)
			names = append(names, QuoteIdentifier(col.Name))
			params = append(params, param)
		}
	}
	if action != "Insert" {
		for i, col := range cols {
			if col.PrimaryKey {
				var value any
				if !original[i].Null {
					value = original[i].Text
				}
				args = append(args, value)
				predicates = append(predicates, QuoteIdentifier(col.Name)+" IS NOT DISTINCT FROM $"+strconv.Itoa(len(args)))
			}
		}
		if len(predicates) == 0 {
			return fmt.Errorf("editing and deleting rows requires a primary key")
		}
		for i, col := range cols {
			var value any
			if !original[i].Null {
				value = original[i].Text
			}
			args = append(args, value)
			predicates = append(predicates, QuoteIdentifier(col.Name)+"::text IS NOT DISTINCT FROM $"+strconv.Itoa(len(args)))
		}
	}
	query := ""
	switch action {
	case "Insert":
		query = "INSERT INTO " + t.Reference(PostgreSQL)
		if len(names) == 0 {
			query += " DEFAULT VALUES"
		} else {
			query += " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(params, ", ") + ")"
		}
	case "Update":
		if len(setters) == 0 {
			return fmt.Errorf("no editable values selected")
		}
		query = "UPDATE " + t.Reference(PostgreSQL) + " SET " + strings.Join(setters, ", ") + " WHERE " + strings.Join(predicates, " AND ")
	case "Delete":
		query = "DELETE FROM " + t.Reference(PostgreSQL) + " WHERE " + strings.Join(predicates, " AND ")
	default:
		return fmt.Errorf("unsupported row action")
	}
	tx, err := c.session.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("row changed or was removed; refresh and try again")
	}
	return tx.Commit()
}

func (c *Client) CopyPostgreSQLCSV(ctx context.Context, t Table, columns string, header bool, delimiter string, input io.Reader, output io.Writer) (int64, error) {
	if c.Engine() != PostgreSQL || c.session == nil {
		return 0, fmt.Errorf("CSV transfer requires a PostgreSQL session")
	}
	if len(delimiter) != 1 || strings.ContainsAny(delimiter, "\r\n\x00") {
		return 0, fmt.Errorf("delimiter must be one non-newline byte")
	}
	query := "COPY " + t.Reference(PostgreSQL)
	if strings.TrimSpace(columns) != "" {
		list, _, err := pgColumnList(columns, nil)
		if err != nil {
			return 0, err
		}
		query += " (" + list + ")"
	}
	if input != nil {
		query += " FROM STDIN"
	} else {
		query += " TO STDOUT"
	}
	query += " WITH (FORMAT csv, HEADER " + strconv.FormatBool(header) + ", DELIMITER " + pgLiteral(delimiter) + ", ENCODING 'UTF8')"
	var count int64
	err := c.session.Raw(func(driver any) error {
		conn, ok := driver.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("PostgreSQL driver unavailable")
		}
		if input != nil {
			tag, err := conn.Conn().PgConn().CopyFrom(ctx, input, query)
			count = tag.RowsAffected()
			return err
		}
		tag, err := conn.Conn().PgConn().CopyTo(ctx, output, query)
		count = tag.RowsAffected()
		return err
	})
	return count, err
}

type TableRelation struct {
	Name                      string
	Source, Target            Table
	Columns, ReferenceColumns []string
}
type DiagramTable struct {
	Table   Table
	Columns []Column
}
type TableDiagram struct {
	Tables    []DiagramTable
	Relations []TableRelation
	Truncated bool
}

func (c *Client) PostgreSQLDiagram(ctx context.Context, t Table) (TableDiagram, error) {
	var graph TableDiagram
	result, err := c.catalogQuery(ctx, `SELECT con.conname,n.nspname,r.relname,rn.nspname,rr.relname,(SELECT json_agg(a.attname ORDER BY k.ord)::text FROM unnest(con.conkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=con.conrelid AND a.attnum=k.num),(SELECT json_agg(a.attname ORDER BY k.ord)::text FROM unnest(con.confkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=con.confrelid AND a.attnum=k.num) FROM pg_constraint con JOIN pg_class r ON r.oid=con.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace JOIN pg_class rr ON rr.oid=con.confrelid JOIN pg_namespace rn ON rn.oid=rr.relnamespace WHERE con.contype='f' AND (con.conrelid=`+pgLiteral(t.Reference(PostgreSQL))+`::regclass OR con.confrelid=`+pgLiteral(t.Reference(PostgreSQL))+`::regclass) ORDER BY con.conname LIMIT 65`)
	if err != nil {
		return graph, err
	}
	tables := []Table{t}
	seen := map[string]bool{t.ID(): true}
	for i, row := range result.Rows {
		if i == 64 {
			graph.Truncated = true
			break
		}
		relation := TableRelation{Name: row[0], Source: Table{Schema: row[1], Name: row[2]}, Target: Table{Schema: row[3], Name: row[4]}}
		json.Unmarshal([]byte(row[5]), &relation.Columns)
		json.Unmarshal([]byte(row[6]), &relation.ReferenceColumns)
		for _, related := range []Table{relation.Source, relation.Target} {
			if !seen[related.ID()] {
				if len(tables) >= 17 {
					graph.Truncated = true
					continue
				}
				tables = append(tables, related)
				seen[related.ID()] = true
			}
		}
		if seen[relation.Source.ID()] && seen[relation.Target.ID()] {
			graph.Relations = append(graph.Relations, relation)
		}
	}
	for _, table := range tables {
		cols, err := c.PostgreSQLColumns(ctx, table)
		if err != nil {
			return graph, err
		}
		if len(cols) > 50 {
			cols = cols[:50]
			graph.Truncated = true
		}
		graph.Tables = append(graph.Tables, DiagramTable{table, cols})
	}
	return graph, nil
}
