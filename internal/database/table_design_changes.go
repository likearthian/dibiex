package database

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type PostgreSQLTableChanges struct {
	Statements  []string
	Destructive bool
}

func constraintRenameOnly(old, next PostgreSQLConstraint) bool {
	if old.Name == next.Name {
		return false
	}
	old.Name = next.Name
	return reflect.DeepEqual(old, next)
}
func indexRenameOnly(old, next PostgreSQLIndex) bool {
	if old.Name == next.Name {
		return false
	}
	old.Name = next.Name
	return reflect.DeepEqual(old, next)
}

func (p PostgreSQLTableChanges) SQL() string {
	if len(p.Statements) == 0 {
		return "-- No changes"
	}
	return "BEGIN;\n\n" + strings.Join(p.Statements, ";\n\n") + ";\n\nCOMMIT;"
}

func newPostgreSQLColumnSQL(col PostgreSQLColumn) (string, error) {
	// Reuse creation validation while emitting only this column's definition.
	if err := pgIdentifier(col.Name, "Column name"); err != nil {
		return "", err
	}
	if !pgType.MatchString(col.Type) && !pgCustomType.MatchString(col.Type) {
		return "", fmt.Errorf("invalid type for column %q", col.Name)
	}
	if err := pgExpression(col.Default); err != nil {
		return "", err
	}
	s := QuoteIdentifier(col.Name) + " " + col.Type
	if col.Identity != "" && col.Identity != "None" {
		if col.Identity != "ALWAYS" && col.Identity != "BY DEFAULT" {
			return "", fmt.Errorf("invalid identity mode")
		}
		switch strings.ToLower(col.Type) {
		case "smallint", "integer", "bigint", "int", "int2", "int4", "int8":
		default:
			return "", fmt.Errorf("identity column must use an integer type")
		}
		if col.Default != "" {
			return "", fmt.Errorf("identity column cannot have a default")
		}
		s += " GENERATED " + col.Identity + " AS IDENTITY"
	} else if col.Default != "" {
		s += " DEFAULT (" + col.Default + ")"
	}
	if col.NotNull {
		s += " NOT NULL"
	}
	if col.Unique {
		s += " UNIQUE"
	}
	return s, nil
}

func designerConstraintSQL(t Table, c PostgreSQLConstraint, columns map[string]bool) (string, error) {
	prefix := "ALTER TABLE " + t.Reference(PostgreSQL) + " ADD "
	if c.Name != "" {
		if err := pgIdentifier(c.Name, "Constraint name"); err != nil {
			return "", err
		}
		prefix += "CONSTRAINT " + QuoteIdentifier(c.Name) + " "
	}
	if c.Kind == "CHECK" {
		if strings.TrimSpace(c.Expression) == "" {
			return "", fmt.Errorf("CHECK expression is required")
		}
		if err := pgExpression(c.Expression); err != nil {
			return "", err
		}
		return prefix + "CHECK (" + c.Expression + ")", nil
	}
	list, names, err := pgColumnList(c.Columns, columns)
	if err != nil {
		return "", err
	}
	switch c.Kind {
	case "PRIMARY KEY", "UNIQUE":
		return prefix + c.Kind + " (" + list + ")", nil
	case "FOREIGN KEY":
		if err := pgIdentifier(c.ReferenceSchema, "Referenced schema"); err != nil {
			return "", err
		}
		if err := pgIdentifier(c.ReferenceTable, "Referenced table"); err != nil {
			return "", err
		}
		refs, refNames, err := pgColumnList(c.ReferenceColumns, nil)
		if err != nil {
			return "", err
		}
		if len(names) != len(refNames) {
			return "", fmt.Errorf("foreign key column counts must match")
		}
		s := prefix + "FOREIGN KEY (" + list + ") REFERENCES " + QuoteIdentifier(c.ReferenceSchema) + "." + QuoteIdentifier(c.ReferenceTable) + " (" + refs + ")"
		if c.OnDelete != "" {
			switch c.OnDelete {
			case "NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT":
			default:
				return "", fmt.Errorf("invalid ON DELETE action")
			}
			s += " ON DELETE " + c.OnDelete
		}
		return s, nil
	}
	return "", fmt.Errorf("unsupported constraint kind")
}

func (s PostgreSQLTableDesign) Changes(d PostgreSQLTable) (PostgreSQLTableChanges, error) {
	var p PostgreSQLTableChanges
	fail := func(message string) (PostgreSQLTableChanges, error) { return p, fmt.Errorf("%s", message) }
	for _, item := range []struct{ name, label string }{{d.Name, "Table name"}, {d.Schema, "Schema"}, {d.Owner, "Owner"}} {
		if err := pgIdentifier(item.name, item.label); err != nil {
			return p, err
		}
	}
	if d.Partitioned != s.Definition.Partitioned || d.PartitionMethod != s.Definition.PartitionMethod || d.PartitionColumns != s.Definition.PartitionColumns {
		return fail("partition layout cannot be changed in place; its existing definition is preserved")
	}
	if len(d.Columns) == 0 || len(d.Columns) > 1600 || len(d.Constraints) > 4096 || len(d.Indexes) > 4096 {
		return fail("table designer object limits exceeded (1–1600 columns, 4096 constraints/indexes)")
	}
	if strings.ContainsRune(d.Comment, 0) {
		return fail("comment cannot contain NUL")
	}
	ref := s.Table.Reference(PostgreSQL)
	originalCols := map[int]PostgreSQLColumn{}
	for _, col := range s.Definition.Columns {
		originalCols[col.Number] = col
	}
	colSnapshots := map[int]PostgreSQLColumnEdit{}
	for _, col := range s.Columns {
		colSnapshots[col.Number] = col
	}
	desiredColumns := map[string]bool{}
	keptCols := map[int]bool{}
	renames := map[string]string{}
	for _, col := range d.Columns {
		if err := pgIdentifier(col.Name, "Column name"); err != nil {
			return p, err
		}
		if desiredColumns[col.Name] {
			return fail("duplicate column name: " + col.Name)
		}
		desiredColumns[col.Name] = true
		if col.Number != 0 {
			old, ok := originalCols[col.Number]
			if !ok || keptCols[col.Number] {
				return fail("invalid or duplicate original column identity")
			}
			keptCols[col.Number] = true
			renames[old.Name] = col.Name
			if col.PrimaryKey != old.PrimaryKey || col.Unique != old.Unique {
				return fail("edit existing primary/unique keys in Constraints")
			}
			if col.Identity != old.Identity {
				return fail("existing identity generation is preserved; only new columns can choose an identity mode")
			}
		}
	}
	constraints := map[int64]PostgreSQLConstraint{}
	indexes := map[int64]PostgreSQLIndex{}
	for _, c := range s.Definition.Constraints {
		constraints[c.ID] = c
	}
	for _, i := range s.Definition.Indexes {
		indexes[i.ID] = i
	}
	keptConstraints := map[int64]PostgreSQLConstraint{}
	keptIndexes := map[int64]PostgreSQLIndex{}
	constraintNames := map[string]bool{}
	indexNames := map[string]bool{}
	for _, c := range d.Constraints {
		if c.Name != "" {
			if constraintNames[c.Name] {
				return fail("duplicate constraint name")
			}
			constraintNames[c.Name] = true
		}
		if c.ID != 0 {
			old, ok := constraints[c.ID]
			if _, dup := keptConstraints[c.ID]; !ok || dup {
				return fail("invalid original constraint identity")
			}
			if old.ReadOnlyReason != "" && !reflect.DeepEqual(c, old) {
				return fail("advanced constraint definition must remain unchanged: " + old.Name)
			}
			keptConstraints[c.ID] = c
		}
	}
	for _, i := range d.Indexes {
		if indexNames[i.Name] {
			return fail("duplicate index name")
		}
		indexNames[i.Name] = true
		if i.ID != 0 {
			old, ok := indexes[i.ID]
			if _, dup := keptIndexes[i.ID]; !ok || dup {
				return fail("invalid original index identity")
			}
			if old.ReadOnlyReason != "" && !reflect.DeepEqual(i, old) {
				return fail("advanced index definition must remain unchanged: " + old.Name)
			}
			keptIndexes[i.ID] = i
		}
	}
	// Drop only explicitly removed/changed constraints and indexes, without CASCADE.
	for _, old := range s.Definition.Constraints {
		next, kept := keptConstraints[old.ID]
		if kept && constraintRenameOnly(old, next) {
			if err := pgIdentifier(next.Name, "Constraint name"); err != nil {
				return p, err
			}
			p.Statements = append(p.Statements, "ALTER TABLE "+ref+" RENAME CONSTRAINT "+QuoteIdentifier(old.Name)+" TO "+QuoteIdentifier(next.Name))
			continue
		}
		if !kept || !reflect.DeepEqual(old, next) {
			p.Destructive = true
			p.Statements = append(p.Statements, "ALTER TABLE "+ref+" DROP CONSTRAINT "+QuoteIdentifier(old.Name))
		}
	}
	for _, old := range s.Definition.Indexes {
		next, kept := keptIndexes[old.ID]
		if old.ConstraintOwned {
			// The owning constraint handles this index. Keeping or removing its
			// row is never a request to independently drop a constraint index.
			if kept && !reflect.DeepEqual(old, next) {
				return fail("constraint indexes are managed in Constraints")
			}
			continue
		}
		if kept && indexRenameOnly(old, next) {
			if err := pgIdentifier(next.Name, "Index name"); err != nil {
				return p, err
			}
			p.Statements = append(p.Statements, "ALTER INDEX "+QuoteIdentifier(s.Table.Schema)+"."+QuoteIdentifier(old.Name)+" RENAME TO "+QuoteIdentifier(next.Name))
			continue
		}
		if !kept || !reflect.DeepEqual(old, next) {
			p.Destructive = true
			p.Statements = append(p.Statements, "DROP INDEX "+QuoteIdentifier(s.Table.Schema)+"."+QuoteIdentifier(old.Name))
		}
	}
	for _, old := range s.Definition.Columns {
		if keptCols[old.Number] {
			continue
		}
		if snapshot := colSnapshots[old.Number]; snapshot.Original.Generated != "" || snapshot.Original.Identity != "" {
			return fail("generated and identity columns are preserved in this editor")
		}
		for _, c := range d.Constraints {
			if c.ID != 0 {
				original := constraints[c.ID]
				for _, name := range strings.Split(original.Columns, ",") {
					if strings.TrimSpace(name) == old.Name {
						return fail("remove constraint " + c.Name + " explicitly before removing column " + old.Name)
					}
				}
			}
		}
		for _, i := range d.Indexes {
			if i.ID != 0 && !i.ConstraintOwned {
				if i.ReadOnlyReason != "" {
					return fail("remove advanced indexes explicitly before removing columns")
				}
				for _, name := range strings.Split(indexes[i.ID].Columns, ",") {
					if strings.TrimSpace(name) == old.Name {
						return fail("remove index " + i.Name + " explicitly before removing column " + old.Name)
					}
				}
			}
		}
		p.Destructive = true
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" DROP COLUMN "+QuoteIdentifier(old.Name))
	}
	var inlinePrimary []string
	for _, col := range d.Columns {
		if col.Number == 0 {
			definition, err := newPostgreSQLColumnSQL(col)
			if err != nil {
				return p, err
			}
			p.Statements = append(p.Statements, "ALTER TABLE "+ref+" ADD COLUMN "+definition)
			if col.PrimaryKey {
				inlinePrimary = append(inlinePrimary, col.Name)
			}
			if strings.ContainsRune(col.Comment, 0) {
				return fail("column comment cannot contain NUL")
			}
			if col.Comment != "" {
				p.Statements = append(p.Statements, "COMMENT ON COLUMN "+ref+"."+QuoteIdentifier(col.Name)+" IS "+pgLiteral(col.Comment))
			}
			continue
		}
		e := colSnapshots[col.Number]
		e.Name, e.Type, e.Default, e.NotNull, e.Comment, e.Using = col.Name, col.Type, col.Default, col.NotNull, col.Comment, col.Using
		statements, err := e.Statements(s.Table)
		if err != nil {
			return p, fmt.Errorf("column %s: %w", col.Name, err)
		}
		p.Statements = append(p.Statements, statements...)
	}
	remap := func(list string) string {
		names := strings.Split(list, ",")
		for i, name := range names {
			name = strings.TrimSpace(name)
			if next, ok := renames[name]; ok {
				name = next
			}
			names[i] = name
		}
		return strings.Join(names, ", ")
	}
	primaryCount := 0
	for _, c := range d.Constraints {
		if c.Kind == "PRIMARY KEY" {
			primaryCount++
		}
		old, existed := constraints[c.ID]
		if existed && (reflect.DeepEqual(old, c) || constraintRenameOnly(old, c)) {
			continue
		}
		if c.ID == 0 && (c.ReadOnlyReason != "" || c.SQL != "") {
			return fail("new constraints require editable fields")
		}
		c.Columns = remap(c.Columns)
		statement, err := designerConstraintSQL(s.Table, c, desiredColumns)
		if err != nil {
			return p, err
		}
		p.Statements = append(p.Statements, statement)
	}
	if len(inlinePrimary) > 0 {
		primaryCount++
		list := make([]string, len(inlinePrimary))
		for i, name := range inlinePrimary {
			list[i] = QuoteIdentifier(name)
		}
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" ADD PRIMARY KEY ("+strings.Join(list, ", ")+")")
	}
	if primaryCount > 1 {
		return fail("a table can have only one primary key")
	}
	for _, i := range d.Indexes {
		old, existed := indexes[i.ID]
		if existed && (reflect.DeepEqual(old, i) || indexRenameOnly(old, i)) || i.ConstraintOwned {
			continue
		}
		if i.ID == 0 && (i.ReadOnlyReason != "" || i.SQL != "") {
			return fail("new indexes require editable fields")
		}
		i.Columns = remap(i.Columns)
		statement, err := i.CreateSQL(s.Table, desiredColumns)
		if err != nil {
			return p, err
		}
		p.Statements = append(p.Statements, statement)
	}
	old := s.Definition
	if d.Unlogged != old.Unlogged {
		if d.Partitioned {
			return fail("logging cannot be changed on a partitioned parent")
		}
		mode := "LOGGED"
		if d.Unlogged {
			mode = "UNLOGGED"
		}
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" SET "+mode)
	}
	if d.Fillfactor != old.Fillfactor || d.Autovacuum != old.Autovacuum {
		if d.Partitioned {
			return fail("storage parameters belong on individual partitions")
		}
		if d.Fillfactor != old.Fillfactor {
			if d.Fillfactor == "" {
				p.Statements = append(p.Statements, "ALTER TABLE "+ref+" RESET (fillfactor)")
			} else {
				n, err := strconv.Atoi(d.Fillfactor)
				if err != nil || n < 10 || n > 100 {
					return fail("fillfactor must be between 10 and 100")
				}
				p.Statements = append(p.Statements, "ALTER TABLE "+ref+" SET (fillfactor = "+strconv.Itoa(n)+")")
			}
		}
		if d.Autovacuum != old.Autovacuum {
			switch d.Autovacuum {
			case "", "Default":
				p.Statements = append(p.Statements, "ALTER TABLE "+ref+" RESET (autovacuum_enabled)")
			case "true", "false":
				p.Statements = append(p.Statements, "ALTER TABLE "+ref+" SET (autovacuum_enabled = "+d.Autovacuum+")")
			default:
				return fail("invalid autovacuum setting")
			}
		}
	}
	if d.Tablespace != old.Tablespace {
		space := d.Tablespace
		if space == "" {
			space = s.DatabaseTablespace
		}
		if err := pgIdentifier(space, "Tablespace"); err != nil {
			return p, err
		}
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" SET TABLESPACE "+QuoteIdentifier(space))
	}
	if d.Comment != old.Comment {
		p.Statements = append(p.Statements, "COMMENT ON TABLE "+ref+" IS "+pgLiteral(d.Comment))
	}
	if d.RowSecurity != old.RowSecurity {
		mode := "DISABLE"
		if d.RowSecurity {
			mode = "ENABLE"
		}
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" "+mode+" ROW LEVEL SECURITY")
	}
	if d.ForceRowSecurity != old.ForceRowSecurity {
		mode := "NO FORCE"
		if d.ForceRowSecurity {
			mode = "FORCE"
		}
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" "+mode+" ROW LEVEL SECURITY")
	}
	if d.Name != old.Name {
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" RENAME TO "+QuoteIdentifier(d.Name))
		ref = QuoteIdentifier(s.Table.Schema) + "." + QuoteIdentifier(d.Name)
	}
	if d.Schema != old.Schema {
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" SET SCHEMA "+QuoteIdentifier(d.Schema))
		ref = QuoteIdentifier(d.Schema) + "." + QuoteIdentifier(d.Name)
	}
	if d.Owner != old.Owner {
		p.Statements = append(p.Statements, "ALTER TABLE "+ref+" OWNER TO "+QuoteIdentifier(d.Owner))
	}
	return p, nil
}

func (c *Client) ApplyPostgreSQLTableDesign(ctx context.Context, original PostgreSQLTableDesign, definition PostgreSQLTable) error {
	changes, err := original.Changes(definition)
	if err != nil {
		return err
	}
	return c.editPostgreSQL(ctx, original.Table, func(tx *sql.Tx) error {
		current, err := readPostgreSQLTableDesign(ctx, tx, original.Table)
		if err != nil {
			return err
		}
		if current.Fingerprint != original.Fingerprint || current.ObjectID != original.ObjectID {
			return fmt.Errorf("table definition changed since it was loaded; reopen the designer to load the current definition")
		}
		for _, statement := range changes.Statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("edit table: %w", err)
			}
		}
		return nil
	})
}
