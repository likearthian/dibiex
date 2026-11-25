package database

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type SQLiteColumn struct {
	Name, Type, Default, Collation             string
	NotNull, PrimaryKey, Unique, AutoIncrement bool
}

type SQLiteConstraint struct {
	Name, Kind, Columns, Expression                      string
	ReferenceTable, ReferenceColumns, OnDelete, OnUpdate string
}

type SQLiteTable struct {
	Name                 string
	Columns              []SQLiteColumn
	Constraints          []SQLiteConstraint
	Strict, WithoutRowID bool
}

var sqliteType = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_]*(?:\s+[a-zA-Z][a-zA-Z0-9_]*)*)(\(\s*[0-9]+(?:\s*,\s*[0-9]+)?\s*\))?$`)

func sqliteIdentifier(name, label string) error {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("%s is required and must be valid text without NUL", label)
	}
	return nil
}

func sqliteColumnList(value string, columns map[string]bool) (string, []string, error) {
	var quoted, names []string
	seen := map[string]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if err := sqliteIdentifier(name, "Column name"); err != nil {
			return "", nil, err
		}
		key := strings.ToLower(name)
		if seen[key] || columns != nil && !columns[key] {
			return "", nil, fmt.Errorf("column %q is repeated or does not exist", name)
		}
		seen[key] = true
		quoted, names = append(quoted, QuoteIdentifier(name)), append(names, name)
	}
	return strings.Join(quoted, ", "), names, nil
}

func (t SQLiteTable) SQL() (string, error) {
	if err := sqliteIdentifier(t.Name, "Table name"); err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.ToLower(t.Name), "sqlite_") {
		return "", fmt.Errorf("table names beginning with sqlite_ are reserved")
	}
	if len(t.Columns) == 0 || len(t.Columns) > 2000 {
		return "", fmt.Errorf("add between 1 and 2000 columns")
	}
	names := map[string]bool{}
	var definitions, primary []string
	auto := false
	for _, col := range t.Columns {
		if err := sqliteIdentifier(col.Name, "Column name"); err != nil {
			return "", err
		}
		key := strings.ToLower(col.Name)
		if names[key] {
			return "", fmt.Errorf("column %q is repeated", col.Name)
		}
		names[key] = true
		typ := strings.TrimSpace(col.Type)
		match := sqliteType.FindStringSubmatch(typ)
		if match == nil {
			return "", fmt.Errorf("column %q needs a valid data type", col.Name)
		}
		if t.Strict {
			switch strings.ToUpper(typ) {
			case "INT", "INTEGER", "REAL", "TEXT", "BLOB", "ANY":
			default:
				return "", fmt.Errorf("STRICT tables require INT, INTEGER, REAL, TEXT, BLOB or ANY")
			}
		}
		definition := QuoteIdentifier(col.Name) + " " + QuoteIdentifier(match[1]) + match[2]
		if col.PrimaryKey {
			primary = append(primary, QuoteIdentifier(col.Name))
		}
		if col.AutoIncrement {
			if auto || !col.PrimaryKey || !strings.EqualFold(typ, "INTEGER") || t.WithoutRowID {
				return "", fmt.Errorf("AUTOINCREMENT requires one INTEGER primary-key column in a rowid table")
			}
			auto = true
			definition += " PRIMARY KEY AUTOINCREMENT"
		}
		if col.NotNull {
			definition += " NOT NULL"
		}
		if col.Unique {
			definition += " UNIQUE"
		}
		if col.Default != "" {
			if err := pgExpression(col.Default); err != nil {
				return "", err
			}
			definition += " DEFAULT (" + col.Default + ")"
		}
		if col.Collation != "" && col.Collation != "Default" {
			switch col.Collation {
			case "BINARY", "NOCASE", "RTRIM":
				definition += " COLLATE " + col.Collation
			default:
				return "", fmt.Errorf("choose BINARY, NOCASE or RTRIM collation")
			}
		}
		definitions = append(definitions, definition)
	}
	if auto && len(primary) != 1 {
		return "", fmt.Errorf("AUTOINCREMENT cannot be used with a composite primary key")
	}
	primaryCount := 0
	if len(primary) > 0 {
		primaryCount++
		if !auto {
			definitions = append(definitions, "PRIMARY KEY ("+strings.Join(primary, ", ")+")")
		}
	}
	constraintNames := map[string]bool{}
	for _, constraint := range t.Constraints {
		prefix := ""
		if constraint.Name != "" {
			if err := sqliteIdentifier(constraint.Name, "Constraint name"); err != nil {
				return "", err
			}
			key := strings.ToLower(constraint.Name)
			if constraintNames[key] {
				return "", fmt.Errorf("constraint %q is repeated", constraint.Name)
			}
			constraintNames[key] = true
			prefix = "CONSTRAINT " + QuoteIdentifier(constraint.Name) + " "
		}
		if constraint.Kind == "CHECK" {
			if strings.TrimSpace(constraint.Expression) == "" {
				return "", fmt.Errorf("enter a CHECK expression")
			}
			if err := pgExpression(constraint.Expression); err != nil {
				return "", err
			}
			definitions = append(definitions, prefix+"CHECK ("+constraint.Expression+")")
			continue
		}
		list, local, err := sqliteColumnList(constraint.Columns, names)
		if err != nil {
			return "", err
		}
		definition := prefix + constraint.Kind + " (" + list + ")"
		switch constraint.Kind {
		case "PRIMARY KEY":
			primaryCount++
		case "UNIQUE":
		case "FOREIGN KEY":
			if err := sqliteIdentifier(constraint.ReferenceTable, "Referenced table"); err != nil {
				return "", err
			}
			refs, remote, err := sqliteColumnList(constraint.ReferenceColumns, nil)
			if err != nil {
				return "", err
			}
			if len(local) != len(remote) {
				return "", fmt.Errorf("foreign-key column counts must match")
			}
			definition += " REFERENCES " + QuoteIdentifier(constraint.ReferenceTable) + " (" + refs + ")"
			for _, rule := range []struct{ name, value string }{{"DELETE", constraint.OnDelete}, {"UPDATE", constraint.OnUpdate}} {
				if rule.value == "" {
					continue
				}
				switch rule.value {
				case "NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT":
					definition += " ON " + rule.name + " " + rule.value
				default:
					return "", fmt.Errorf("choose a valid foreign-key action")
				}
			}
		default:
			return "", fmt.Errorf("choose a supported SQLite constraint")
		}
		definitions = append(definitions, definition)
	}
	if primaryCount > 1 {
		return "", fmt.Errorf("a table can have only one primary key")
	}
	if t.WithoutRowID && primaryCount == 0 {
		return "", fmt.Errorf("WITHOUT ROWID requires a primary key")
	}
	sql := "CREATE TABLE " + QuoteIdentifier("main") + "." + QuoteIdentifier(t.Name) + " (\n  " + strings.Join(definitions, ",\n  ") + "\n)"
	var options []string
	if t.WithoutRowID {
		options = append(options, "WITHOUT ROWID")
	}
	if t.Strict {
		options = append(options, "STRICT")
	}
	if len(options) > 0 {
		sql += " " + strings.Join(options, ", ")
	}
	return sql + ";", nil
}

func (c *Client) CreateSQLiteTable(ctx context.Context, table SQLiteTable) error {
	if c.Engine() != SQLite {
		return fmt.Errorf("SQLite table designer requires a SQLite connection")
	}
	statement, err := table.SQL()
	if err != nil {
		return err
	}
	session, err := c.NewQuerySession(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	tx, err := session.session.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	return tx.Commit()
}
