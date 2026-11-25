package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

type PostgreSQLColumnEdit struct {
	Original                            Column
	TableOID                            int64
	Number                              int
	Name, Type, Default, Comment, Using string
	NotNull                             bool
}

func (e PostgreSQLColumnEdit) Statements(t Table) ([]string, error) {
	for _, item := range []struct{ name, label string }{{t.Schema, "Schema"}, {t.Name, "Table"}, {e.Original.Name, "Original column"}, {e.Name, "Column name"}} {
		if err := pgIdentifier(item.name, item.label); err != nil {
			return nil, err
		}
	}
	if strings.ContainsRune(e.Comment, 0) {
		return nil, fmt.Errorf("comments cannot contain NUL")
	}
	protected := e.Original.Generated != "" || e.Original.Identity != ""
	typeChanged := e.Type != e.Original.Type
	defaultChanged := e.Default != e.Original.Default
	nullChanged := e.NotNull != (e.Original.Nullable == "NO")
	if protected && (typeChanged || defaultChanged || nullChanged || strings.TrimSpace(e.Using) != "") {
		return nil, fmt.Errorf("generated and identity columns support only renaming and comments in this editor")
	}
	if e.Original.PrimaryKey && !e.NotNull {
		return nil, fmt.Errorf("a primary-key column cannot be nullable")
	}
	if typeChanged {
		if !pgType.MatchString(e.Type) && !pgCustomType.MatchString(e.Type) {
			return nil, fmt.Errorf("enter a valid PostgreSQL data type")
		}
		for _, serial := range []string{"serial", "smallserial", "bigserial"} {
			if strings.EqualFold(e.Type, serial) {
				return nil, fmt.Errorf("serial cannot be used as an ALTER COLUMN type")
			}
		}
	}
	if strings.TrimSpace(e.Using) != "" && !typeChanged {
		return nil, fmt.Errorf("USING applies only when changing the data type")
	}
	if typeChanged || defaultChanged {
		if err := pgExpression(e.Default); err != nil {
			return nil, err
		}
	}
	if err := pgExpression(e.Using); err != nil {
		return nil, err
	}
	ref := t.Reference(PostgreSQL)
	column := QuoteIdentifier(e.Original.Name)
	prefix := "ALTER TABLE " + ref + " ALTER COLUMN " + column
	var statements []string
	// USING converts stored values, not the existing default. Drop and restore
	// defaults around a type change, all within the same transaction.
	if e.Original.Default != "" && (typeChanged || defaultChanged) {
		statements = append(statements, prefix+" DROP DEFAULT")
	}
	if typeChanged {
		statement := prefix + " TYPE " + e.Type
		if strings.TrimSpace(e.Using) != "" {
			statement += " USING (" + e.Using + ")"
		}
		statements = append(statements, statement)
	}
	if e.Default != "" && (typeChanged || defaultChanged) {
		statements = append(statements, prefix+" SET DEFAULT ("+e.Default+")")
	}
	if nullChanged {
		mode := "DROP"
		if e.NotNull {
			mode = "SET"
		}
		statements = append(statements, prefix+" "+mode+" NOT NULL")
	}
	if e.Name != e.Original.Name {
		statements = append(statements, "ALTER TABLE "+ref+" RENAME COLUMN "+column+" TO "+QuoteIdentifier(e.Name))
	}
	if e.Comment != e.Original.Description {
		statements = append(statements, "COMMENT ON COLUMN "+ref+"."+QuoteIdentifier(e.Name)+" IS "+pgLiteral(e.Comment))
	}
	return statements, nil
}

func (e PostgreSQLColumnEdit) SQL(t Table) (string, error) {
	statements, err := e.Statements(t)
	if err != nil {
		return "", err
	}
	if len(statements) == 0 {
		return "-- No changes", nil
	}
	return "BEGIN;\n" + strings.Join(statements, ";\n") + ";\nCOMMIT;", nil
}

type postgresRowReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readPostgreSQLColumn(ctx context.Context, reader postgresRowReader, t Table, name string) (PostgreSQLColumnEdit, error) {
	var e PostgreSQLColumnEdit
	var notNull bool
	err := reader.QueryRowContext(ctx, `SELECT a.attrelid::bigint,a.attnum,a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod),a.attnotnull,
 COALESCE(pg_get_expr(d.adbin,d.adrelid),''),a.attidentity::text,a.attgenerated::text,COALESCE(col_description(a.attrelid,a.attnum),''),
 EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conrelid=a.attrelid AND k.contype='p' AND a.attnum=ANY(k.conkey))
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attrelid=to_regclass($1) AND a.attname=$2 AND a.attnum>0 AND NOT a.attisdropped AND c.relkind IN ('r','p')`, t.Reference(PostgreSQL), name).Scan(&e.TableOID, &e.Number, &e.Original.Name, &e.Original.Type, &notNull, &e.Original.Default, &e.Original.Identity, &e.Original.Generated, &e.Original.Description, &e.Original.PrimaryKey)
	if err == sql.ErrNoRows {
		return e, fmt.Errorf("table or column no longer exists, or is not an editable table")
	}
	if err != nil {
		return e, err
	}
	e.Original.Nullable = "YES"
	if notNull {
		e.Original.Nullable = "NO"
	}
	e.Name, e.Type, e.Default, e.Comment, e.NotNull = e.Original.Name, e.Original.Type, e.Original.Default, e.Original.Description, notNull
	return e, nil
}

func (c *Client) PostgreSQLColumnDefinition(ctx context.Context, t Table, name string) (PostgreSQLColumnEdit, error) {
	if c.Engine() != PostgreSQL {
		return PostgreSQLColumnEdit{}, fmt.Errorf("column editing requires PostgreSQL")
	}
	if c.session != nil {
		return readPostgreSQLColumn(ctx, c.session, t, name)
	}
	return readPostgreSQLColumn(ctx, c.db, t, name)
}

// Own one idle connection and transaction; never commit an editor transaction.
func (c *Client) editPostgreSQL(ctx context.Context, t Table, apply func(*sql.Tx) error) error {
	if c.Engine() != PostgreSQL {
		return fmt.Errorf("table editing requires PostgreSQL")
	}
	if err := pgIdentifier(t.Schema, "Schema"); err != nil {
		return err
	}
	if err := pgIdentifier(t.Name, "Table"); err != nil {
		return err
	}
	conn := c.session
	if conn == nil {
		var err error
		conn, err = c.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
	}
	if err := conn.Raw(func(driver any) error {
		pg, ok := driver.(*stdlib.Conn)
		if !ok || pg.Conn().PgConn().TxStatus() != 'I' {
			return fmt.Errorf("finish the existing transaction before editing a table")
		}
		return nil
	}); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "LOCK TABLE "+t.Reference(PostgreSQL)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	if err = apply(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Client) AlterPostgreSQLColumn(ctx context.Context, t Table, e PostgreSQLColumnEdit) error {
	statements, err := e.Statements(t)
	if err != nil {
		return err
	}
	return c.editPostgreSQL(ctx, t, func(tx *sql.Tx) error {
		current, err := readPostgreSQLColumn(ctx, tx, t, e.Original.Name)
		if err != nil {
			return err
		}
		a, b := e.Original, current.Original
		if e.TableOID != current.TableOID || e.Number != current.Number || a.Type != b.Type || a.Default != b.Default || a.Nullable != b.Nullable || a.Identity != b.Identity || a.Generated != b.Generated || a.Description != b.Description || a.PrimaryKey != b.PrimaryKey {
			return fmt.Errorf("column changed since this dialog was opened; reopen it to load the current definition")
		}
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("edit column: %w", err)
			}
		}
		return nil
	})
}

func (a TableAction) propertyStatements(t Table) ([]string, error) {
	for _, item := range []struct{ name, label string }{{t.Schema, "Schema"}, {t.Name, "Original table"}, {a.Name, "Table name"}} {
		if err := pgIdentifier(item.name, item.label); err != nil {
			return nil, err
		}
	}
	if strings.ContainsRune(a.Expression, 0) {
		return nil, fmt.Errorf("comments cannot contain NUL")
	}
	ref := t.Reference(PostgreSQL)
	var statements []string
	if a.Name != t.Name {
		statements = append(statements, "ALTER TABLE "+ref+" RENAME TO "+QuoteIdentifier(a.Name))
		ref = QuoteIdentifier(t.Schema) + "." + QuoteIdentifier(a.Name)
	}
	if a.ReferenceSchema != "" && a.ReferenceSchema != t.Schema {
		if err := pgIdentifier(a.ReferenceSchema, "Target schema"); err != nil {
			return nil, err
		}
		statements = append(statements, "ALTER TABLE "+ref+" SET SCHEMA "+QuoteIdentifier(a.ReferenceSchema))
		ref = QuoteIdentifier(a.ReferenceSchema) + "." + QuoteIdentifier(a.Name)
	}
	statements = append(statements, "COMMENT ON TABLE "+ref+" IS "+pgLiteral(a.Expression))
	mode := "DISABLE"
	if a.RowSecurity {
		mode = "ENABLE"
	}
	statements = append(statements, "ALTER TABLE "+ref+" "+mode+" ROW LEVEL SECURITY")
	mode = "NO FORCE"
	if a.ForceRowSecurity {
		if !a.RowSecurity {
			return nil, fmt.Errorf("enable row security before forcing it")
		}
		mode = "FORCE"
	}
	statements = append(statements, "ALTER TABLE "+ref+" "+mode+" ROW LEVEL SECURITY")
	if a.Function != "" {
		if err := pgIdentifier(a.Function, "Owner"); err != nil {
			return nil, err
		}
		statements = append(statements, "ALTER TABLE "+ref+" OWNER TO "+QuoteIdentifier(a.Function))
	}
	return statements, nil
}

func readPostgreSQLTableProperties(ctx context.Context, reader postgresRowReader, t Table) (TableAction, error) {
	a := TableAction{Kind: "Properties", Name: t.Name, ReferenceSchema: t.Schema}
	err := reader.QueryRowContext(ctx, `SELECT oid::bigint,pg_get_userbyid(relowner),COALESCE(obj_description(oid,'pg_class'),''),relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid=to_regclass($1) AND relkind IN ('r','p')`, t.Reference(PostgreSQL)).Scan(&a.ObjectID, &a.Function, &a.Expression, &a.RowSecurity, &a.ForceRowSecurity)
	if err == sql.ErrNoRows {
		return a, fmt.Errorf("table no longer exists or is not editable")
	}
	return a, err
}

func (c *Client) AlterPostgreSQLTable(ctx context.Context, t Table, original, edit TableAction) error {
	statements, err := edit.propertyStatements(t)
	if err != nil {
		return err
	}
	return c.editPostgreSQL(ctx, t, func(tx *sql.Tx) error {
		current, err := readPostgreSQLTableProperties(ctx, tx, t)
		if err != nil {
			return err
		}
		if original.ObjectID != current.ObjectID || original.Expression != current.Expression || original.Function != current.Function || original.RowSecurity != current.RowSecurity || original.ForceRowSecurity != current.ForceRowSecurity {
			return fmt.Errorf("table changed since this dialog was opened; reopen it to load the current properties")
		}
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("edit table: %w", err)
			}
		}
		return nil
	})
}
