package app

import (
	"strings"

	"dbcraft/internal/database"
)

// Non-unreserved PostgreSQL 18 keywords, conservatively quoted because the same
// completion can be used as a schema, relation, column or alias.
// https://github.com/postgres/postgres/blob/REL_18_STABLE/src/include/parser/kwlist.h
var postgresCompletionKeywords = func() map[string]bool {
	words := `all analyse analyze and any array as asc asymmetric authorization
	between bigint binary bit boolean both case cast char character check coalesce
	collate collation column concurrently constraint create cross current_catalog
	current_date current_role current_schema current_time current_timestamp current_user
	dec decimal default deferrable desc distinct do else end except exists extract
	false fetch float for foreign freeze from full grant greatest group grouping having
	ilike in initially inner inout int integer intersect interval into is isnull join
	json json_array json_arrayagg json_exists json_object json_objectagg json_query
	json_scalar json_serialize json_table json_value lateral leading least left like
	limit localtime localtimestamp merge_action national natural nchar none normalize
	not notnull null nullif numeric offset on only or order out outer overlaps overlay
	placing position precision primary real references returning right row select
	session_user setof similar smallint some substring symmetric system_user table
	tablesample then time timestamp to trailing treat trim true union unique user using
	values varchar variadic verbose when where window with xmlattributes xmlconcat
	xmlelement xmlexists xmlforest xmlnamespaces xmlparse xmlpi xmlroot xmlserialize xmltable`
	keywords := map[string]bool{}
	for _, word := range strings.Fields(words) {
		keywords[word] = true
	}
	return keywords
}()

func barePostgreSQLCompletion(name string) bool {
	if name == "" || postgresCompletionKeywords[name] {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c == '_' || i > 0 && (c >= '0' && c <= '9' || c == '$') {
			continue
		}
		return false
	}
	return true
}

func completionTableReference(table database.Table, engine database.Engine) string {
	if engine != database.PostgreSQL {
		return table.Reference(engine)
	}
	name := completionIdentifier(table.Name, engine)
	if table.Schema != "" {
		name = completionIdentifier(table.Schema, engine) + "." + name
	}
	return name
}
