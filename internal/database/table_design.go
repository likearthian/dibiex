package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type PostgreSQLIndex struct {
	ID                               int64
	Name, Method, Columns, Predicate string
	Unique, ConstraintOwned          bool
	Advanced                         bool
	SQL, ReadOnlyReason              string
}

func defaultIndexOrdering(value any) bool {
	options, ok := value.([]any)
	if !ok {
		return false
	}
	for _, option := range options {
		if option != float64(0) {
			return false
		}
	}
	return true
}

func (i PostgreSQLIndex) CreateSQL(t Table, columns map[string]bool) (string, error) {
	if err := pgIdentifier(i.Name, "Index name"); err != nil {
		return "", err
	}
	method := i.Method
	if method == "" {
		method = "btree"
	}
	switch method {
	case "btree", "hash", "gin", "gist", "spgist", "brin":
	default:
		return "", fmt.Errorf("unsupported index method")
	}
	if i.Unique && method != "btree" {
		return "", fmt.Errorf("unique indexes require btree")
	}
	list, _, err := pgColumnList(i.Columns, columns)
	if err != nil {
		return "", err
	}
	if err = pgExpression(i.Predicate); err != nil {
		return "", err
	}
	prefix := "CREATE "
	if i.Unique {
		prefix += "UNIQUE "
	}
	statement := prefix + "INDEX " + QuoteIdentifier(i.Name) + " ON " + t.Reference(PostgreSQL) + " USING " + method + " (" + list + ")"
	if strings.TrimSpace(i.Predicate) != "" {
		statement += " WHERE (" + i.Predicate + ")"
	}
	return statement, nil
}

type PostgreSQLTableDesign struct {
	Table                                         Table
	ObjectID                                      int64
	Definition                                    PostgreSQLTable
	Columns                                       []PostgreSQLColumnEdit
	PartitionSQL, DatabaseTablespace, Fingerprint string
}

func (c *Client) LoadPostgreSQLTableDesign(ctx context.Context, t Table) (PostgreSQLTableDesign, error) {
	if c.Engine() != PostgreSQL {
		return PostgreSQLTableDesign{}, fmt.Errorf("table designer requires PostgreSQL")
	}
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return PostgreSQLTableDesign{}, err
	}
	defer tx.Rollback()
	snapshot, err := readPostgreSQLTableDesign(ctx, tx, t)
	if err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit()
}

func readPostgreSQLTableDesign(ctx context.Context, tx *sql.Tx, t Table) (PostgreSQLTableDesign, error) {
	s := PostgreSQLTableDesign{Table: t, Definition: PostgreSQLTable{Schema: t.Schema, Name: t.Name, Autovacuum: "Default", PartitionMethod: "RANGE"}}
	d := &s.Definition
	var options, kind, persistence, partitionMethod string
	err := tx.QueryRowContext(ctx, `SELECT c.oid::bigint,c.relkind::text,c.relpersistence::text,pg_get_userbyid(c.relowner),COALESCE(obj_description(c.oid,'pg_class'),''),
 COALESCE(ts.spcname,''),c.relrowsecurity,c.relforcerowsecurity,COALESCE(to_jsonb(c.reloptions)::text,'[]'),COALESCE(pg_get_partkeydef(c.oid),''),COALESCE(p.partstrat::text,''),
 COALESCE((SELECT string_agg(a.attname,', ' ORDER BY k.ord) FROM unnest(p.partattrs) WITH ORDINALITY k(num,ord) LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num),''),
 (SELECT x.spcname FROM pg_database db JOIN pg_tablespace x ON x.oid=db.dattablespace WHERE db.datname=current_database())
 FROM pg_class c LEFT JOIN pg_tablespace ts ON ts.oid=c.reltablespace LEFT JOIN pg_partitioned_table p ON p.partrelid=c.oid
 WHERE c.oid=to_regclass($1) AND c.relkind IN ('r','p')`, t.Reference(PostgreSQL)).Scan(&s.ObjectID, &kind, &persistence, &d.Owner, &d.Comment, &d.Tablespace, &d.RowSecurity, &d.ForceRowSecurity, &options, &s.PartitionSQL, &partitionMethod, &d.PartitionColumns, &s.DatabaseTablespace)
	if err == sql.ErrNoRows {
		return s, fmt.Errorf("table no longer exists or is not an editable table")
	}
	if err != nil {
		return s, err
	}
	d.Partitioned, d.Unlogged = kind == "p", persistence == "u"
	if method := map[string]string{"r": "RANGE", "l": "LIST", "h": "HASH"}[partitionMethod]; method != "" {
		d.PartitionMethod = method
	}
	var reloptions []string
	if err = json.Unmarshal([]byte(options), &reloptions); err != nil {
		return s, err
	}
	for _, option := range reloptions {
		parts := strings.SplitN(option, "=", 2)
		if len(parts) == 2 {
			switch parts[0] {
			case "fillfactor":
				d.Fillfactor = parts[1]
			case "autovacuum_enabled":
				d.Autovacuum = parts[1]
			}
		}
	}
	var rawStructure []string
	rawStructure = append(rawStructure, options)
	payloadBytes := len(options) + len(d.Comment) + len(s.PartitionSQL)
	withinBudget := func(values ...string) bool {
		for _, value := range values {
			payloadBytes += len(value)
		}
		return payloadBytes <= 8<<20
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.attnum,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,COALESCE(pg_get_expr(x.adbin,x.adrelid),''),a.attidentity::text,a.attgenerated::text,COALESCE(col_description(a.attrelid,a.attnum),''),
 EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conrelid=a.attrelid AND k.contype='p' AND a.attnum=ANY(k.conkey)),to_jsonb(a)::text
 FROM pg_attribute a LEFT JOIN pg_attrdef x ON x.adrelid=a.attrelid AND x.adnum=a.attnum WHERE a.attrelid=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 1601`, s.ObjectID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		e := PostgreSQLColumnEdit{TableOID: s.ObjectID}
		var notNull bool
		var raw string
		if err = rows.Scan(&e.Number, &e.Name, &e.Type, &notNull, &e.Default, &e.Original.Identity, &e.Original.Generated, &e.Comment, &e.Original.PrimaryKey, &raw); err != nil {
			rows.Close()
			return s, err
		}
		e.NotNull = notNull
		if !withinBudget(raw, e.Default, e.Comment, e.Type) {
			rows.Close()
			return s, fmt.Errorf("table definition exceeds the 8 MiB designer limit")
		}
		e.Original.Name, e.Original.Type, e.Original.Default, e.Original.Description = e.Name, e.Type, e.Default, e.Comment
		e.Original.Nullable = "YES"
		if notNull {
			e.Original.Nullable = "NO"
		}
		s.Columns = append(s.Columns, e)
		rawStructure = append(rawStructure, raw)
		identity := "None"
		if e.Original.Identity == "a" {
			identity = "ALWAYS"
		}
		if e.Original.Identity == "d" {
			identity = "BY DEFAULT"
		}
		d.Columns = append(d.Columns, PostgreSQLColumn{Number: e.Number, Name: e.Name, Type: e.Type, Default: e.Default, NotNull: notNull, Identity: identity, PrimaryKey: e.Original.PrimaryKey, Comment: e.Comment})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	if len(s.Columns) > 1600 {
		return s, fmt.Errorf("table exceeds the 1600 column designer limit")
	}
	rows, err = tx.QueryContext(ctx, `SELECT k.oid::bigint,k.conname,k.contype::text,pg_get_constraintdef(k.oid,true),COALESCE(pg_get_expr(k.conbin,k.conrelid),''),
 COALESCE((SELECT jsonb_agg(a.attname ORDER BY u.ord) FROM unnest(k.conkey) WITH ORDINALITY u(num,ord) JOIN pg_attribute a ON a.attrelid=k.conrelid AND a.attnum=u.num),'[]')::text,
 COALESCE(n.nspname,''),COALESCE(r.relname,''),COALESCE((SELECT jsonb_agg(a.attname ORDER BY u.ord) FROM unnest(k.confkey) WITH ORDINALITY u(num,ord) JOIN pg_attribute a ON a.attrelid=k.confrelid AND a.attnum=u.num),'[]')::text,k.confdeltype::text,(to_jsonb(k)||jsonb_build_object('comment',obj_description(k.oid,'pg_constraint')))::text
 FROM pg_constraint k LEFT JOIN pg_class r ON r.oid=k.confrelid LEFT JOIN pg_namespace n ON n.oid=r.relnamespace WHERE k.conrelid=$1 ORDER BY k.oid LIMIT 4097`, s.ObjectID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var constraint PostgreSQLConstraint
		var kind, cols, refs, onDelete, raw string
		if err = rows.Scan(&constraint.ID, &constraint.Name, &kind, &constraint.SQL, &constraint.Expression, &cols, &constraint.ReferenceSchema, &constraint.ReferenceTable, &refs, &onDelete, &raw); err != nil {
			rows.Close()
			return s, err
		}
		constraint.Kind = map[string]string{"p": "PRIMARY KEY", "u": "UNIQUE", "f": "FOREIGN KEY", "c": "CHECK"}[kind]
		if !withinBudget(raw, constraint.SQL, constraint.Expression, cols, refs) {
			rows.Close()
			return s, fmt.Errorf("table definition exceeds the 8 MiB designer limit")
		}
		var names, referenceNames []string
		if err = json.Unmarshal([]byte(cols), &names); err != nil {
			rows.Close()
			return s, err
		}
		if err = json.Unmarshal([]byte(refs), &referenceNames); err != nil {
			rows.Close()
			return s, err
		}
		constraint.Columns, constraint.ReferenceColumns = strings.Join(names, ", "), strings.Join(referenceNames, ", ")
		constraint.OnDelete = map[string]string{"a": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}[onDelete]
		var flags map[string]any
		if err = json.Unmarshal([]byte(raw), &flags); err != nil {
			rows.Close()
			return s, err
		}
		if constraint.Kind == "" || flags["condeferrable"] == true || flags["convalidated"] == false || flags["conislocal"] == false || fmt.Sprint(flags["conparentid"]) != "0" || kind == "f" && (flags["confupdtype"] != "a" || flags["confmatchtype"] != "s") || strings.Contains(constraint.SQL, "NULLS NOT DISTINCT") || strings.Contains(constraint.SQL, "NO INHERIT") || strings.Contains(constraint.SQL, " INCLUDE ") || flags["conenforced"] == false || flags["comment"] != nil && flags["comment"] != "" {
			constraint.ReadOnlyReason = "This constraint has additional PostgreSQL attributes; its exact definition is preserved unless explicitly removed."
		}
		for _, name := range append(names, referenceNames...) {
			if strings.Contains(name, ",") || strings.TrimSpace(name) != name {
				constraint.ReadOnlyReason = "Column names require the exact native constraint definition."
			}
		}
		if constraint.Kind == "CHECK" && pgExpression(constraint.Expression) != nil {
			constraint.ReadOnlyReason = "This expression requires its exact native SQL definition."
		}
		d.Constraints = append(d.Constraints, constraint)
		rawStructure = append(rawStructure, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	if len(d.Constraints) > 4096 {
		return s, fmt.Errorf("table exceeds the 4096 constraint designer limit")
	}
	rows, err = tx.QueryContext(ctx, `SELECT x.oid::bigint,x.relname,m.amname,i.indisunique,COALESCE(pg_get_expr(i.indpred,i.indrelid),''),pg_get_indexdef(i.indexrelid),
 COALESCE((SELECT jsonb_agg(a.attname ORDER BY u.ord) FROM unnest(i.indkey) WITH ORDINALITY u(num,ord) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=u.num),'[]')::text,
 EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conindid=i.indexrelid AND k.conrelid=i.indrelid),(to_jsonb(i)||jsonb_build_object('comment',obj_description(x.oid,'pg_class')))::text,to_jsonb(x)::text,
 EXISTS(SELECT 1 FROM unnest(i.indclass) z(oid) JOIN pg_opclass o ON o.oid=z.oid WHERE NOT o.opcdefault),
 EXISTS(SELECT 1 FROM unnest(i.indcollation) WITH ORDINALITY z(oid,ord) JOIN unnest(i.indkey) WITH ORDINALITY k(num,ord) USING(ord) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.num WHERE z.oid<>a.attcollation)
 FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_am m ON m.oid=x.relam WHERE i.indrelid=$1 ORDER BY x.oid LIMIT 4097`, s.ObjectID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var index PostgreSQLIndex
		var namesJSON, raw, relation string
		var customOp, customColl bool
		if err = rows.Scan(&index.ID, &index.Name, &index.Method, &index.Unique, &index.Predicate, &index.SQL, &namesJSON, &index.ConstraintOwned, &raw, &relation, &customOp, &customColl); err != nil {
			rows.Close()
			return s, err
		}
		var names []string
		if !withinBudget(raw, relation, index.SQL, namesJSON) {
			rows.Close()
			return s, fmt.Errorf("table definition exceeds the 8 MiB designer limit")
		}
		var flags, rel map[string]any
		if err = json.Unmarshal([]byte(namesJSON), &names); err != nil {
			rows.Close()
			return s, err
		}
		if err = json.Unmarshal([]byte(raw), &flags); err != nil {
			rows.Close()
			return s, err
		}
		if err = json.Unmarshal([]byte(relation), &rel); err != nil {
			rows.Close()
			return s, err
		}
		index.Columns = strings.Join(names, ", ")
		index.Advanced = flags["indexprs"] != nil || flags["indnkeyatts"] != flags["indnatts"] || flags["indisvalid"] != true || flags["indisreplident"] == true || flags["indisclustered"] == true || flags["indnullsnotdistinct"] == true || customOp || customColl || fmt.Sprint(rel["reltablespace"]) != "0" || rel["reloptions"] != nil || !defaultIndexOrdering(flags["indoption"]) || pgExpression(index.Predicate) != nil || flags["comment"] != nil && flags["comment"] != ""
		if index.Advanced {
			index.ReadOnlyReason = "This index has additional PostgreSQL attributes; its exact definition is preserved unless explicitly removed."
		}
		if index.ConstraintOwned {
			index.ReadOnlyReason = "Managed by its table constraint; edit the constraint instead."
		}
		for _, name := range names {
			if strings.Contains(name, ",") || strings.TrimSpace(name) != name {
				index.ReadOnlyReason = "Column names require the exact native index definition."
			}
		}
		d.Indexes = append(d.Indexes, index)
		rawStructure = append(rawStructure, raw)
		// Exclude mutable storage statistics from pg_class, retaining structural settings.
		rawStructure = append(rawStructure, fmt.Sprint(rel["reloptions"], rel["reltablespace"], rel["relam"]))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	if len(d.Indexes) > 4096 {
		return s, fmt.Errorf("table exceeds the 4096 index designer limit")
	}
	for i := range d.Constraints {
		constraint := &d.Constraints[i]
		if constraint.Kind == "UNIQUE" && !strings.Contains(constraint.Columns, ",") {
			for j := range d.Columns {
				if d.Columns[j].Name == constraint.Columns {
					d.Columns[j].Unique = true
				}
			}
		}
		for _, index := range d.Indexes {
			if index.ConstraintOwned && index.Advanced && index.Name == constraint.Name {
				constraint.ReadOnlyReason = "The backing index has additional PostgreSQL attributes; its exact definition is preserved."
			}
		}
	}
	encoded, err := json.Marshal(struct {
		OID        int64
		Definition PostgreSQLTable
		Columns    []PostgreSQLColumnEdit
		Partition  string
		Raw        []string
	}{s.ObjectID, s.Definition, s.Columns, s.PartitionSQL, rawStructure})
	if err != nil {
		return s, err
	}
	if len(encoded) > 8<<20 {
		return s, fmt.Errorf("table definition exceeds the 8 MiB designer limit")
	}
	s.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return s, nil
}
