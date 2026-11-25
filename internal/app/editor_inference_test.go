package app

import (
	"strings"
	"testing"

	"dbcraft/internal/database"
)

func TestSQLScopedAliasesAndInference(t *testing.T) {
	schemas, metadata := completionFixture()
	for _, tc := range []struct {
		sql          string
		want, absent []string
	}{
		{`SELECT x.| FROM (SELECT id AS user_id, "display name" FROM users) x`, []string{"user_id", "display name"}, []string{"id", "order_id"}},
		{`WITH recent AS (SELECT id AS user_id FROM users) SELECT r.| FROM recent r`, []string{"user_id"}, []string{"id", "order_id"}},
		{`WITH a AS (SELECT id FROM users), b AS (SELECT id AS user_id FROM a) SELECT b.| FROM b`, []string{"user_id"}, []string{"id"}},
		{`WITH a(user_id, label) AS (SELECT id, "display name" FROM users) SELECT a.| FROM a`, []string{"user_id", "label"}, []string{"id", "display name"}},
		{`WITH a AS (SELECT u.* FROM users u) SELECT a.| FROM a`, []string{"id", "display name", "étiquette"}, []string{"order_id"}},
		{`SELECT d.| FROM (SELECT * FROM users) d`, []string{"id", "display name"}, []string{"order_id"}},
		{`SELECT d.| FROM (SELECT id, COUNT(*) AS total FROM users GROUP BY id) d`, []string{"id", "total"}, []string{"display name"}},
		{`SELECT d.| FROM (SELECT id, "display name" FROM users) AS d(user_id, label)`, []string{"user_id", "label"}, []string{"id", "display name"}},
		{`WITH users AS (SELECT 1 AS synthetic) SELECT u.| FROM users u`, []string{"synthetic"}, []string{"id", "display name"}},
		{`WITH a AS (SELECT 1 AS one) SELECT d.| FROM (WITH a AS (SELECT 2 AS two) SELECT * FROM a) d`, []string{"two"}, []string{"one"}},
		{`WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r) SELECT r.| FROM r`, []string{"n"}, nil},
		{`WITH RECURSIVE r AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM r) SELECT r.| FROM r`, []string{"n"}, nil},
		{`SELECT u.id FROM users u WHERE EXISTS (SELECT u.| FROM orders u)`, []string{"order_id"}, []string{"id", "display name"}},
		{`SELECT u.id FROM users u WHERE EXISTS (SELECT u.| FROM orders o)`, []string{"id", "display name"}, []string{"order_id"}},
		{`SELECT u.| FROM users u WHERE EXISTS (SELECT * FROM orders u)`, []string{"id", "display name"}, []string{"order_id"}},
		{`SELECT users.| FROM users u`, nil, []string{"id", "display name"}},
		{`SELECT u.| FROM users u UNION ALL SELECT o.order_id FROM orders o`, []string{"id"}, []string{"order_id"}},
		{`SELECT u.id FROM users u UNION ALL SELECT o.| FROM orders o`, []string{"order_id"}, []string{"id"}},
		{`SELECT d.| FROM (SELECT id AS user_id FROM users UNION ALL SELECT order_id FROM orders) d`, []string{"user_id"}, []string{"order_id", "id"}},
		{`WITH a AS (SELECT 1 AS n) SELECT * FROM a|`, []string{"a"}, nil},
		{`SELECT u.id FROM users u WHERE EXISTS (SELECT | FROM orders o)`, []string{"u", "o", "id", "order_id"}, nil},
		{`SELECT d.| FROM (SELECT id + 1, COUNT(*) FROM users) d`, nil, []string{"id", "COUNT"}},
		{`SELECT d.| FROM (SELECT CAST(id AS TEXT) AS text_id FROM users) d`, []string{"text_id"}, []string{"TEXT", "id"}},
		{`SELECT u.id FROM users u UNION ALL SELECT o.order_id FROM orders o WHERE EXISTS (SELECT o.|)`, []string{"order_id"}, []string{"id"}},
		{`SELECT d.| FROM users u JOIN LATERAL (SELECT u.*) d ON true`, []string{"id", "display name"}, []string{"order_id"}},
		{`SELECT d.| FROM users u JOIN (SELECT u.*) d ON true`, nil, []string{"id", "display name"}},
		{`SELECT d.| FROM users u JOIN LATERAL (SELECT o.*) d ON true JOIN orders o ON true`, nil, []string{"order_id"}},
		{`SELECT id AS user_id FROM users ORDER BY user_|`, []string{"user_id"}, nil},
		{`SELECT id AS user_id FROM users WHERE user_|`, nil, []string{"user_id"}},
		{`SELECT d.| FROM (SELECT a.id, b.id FROM users a JOIN users b ON true) d`, nil, []string{"id"}},
		{`SELECT "select".| FROM users AS "select"`, []string{"id"}, []string{"order_id"}},
		{`SELECT "U".| FROM users AS "U"`, []string{"id"}, []string{"order_id"}},
		{`SELECT U.| FROM users AS "U"`, nil, []string{"id"}},
		{`SELECT U.| FROM USERS AS U`, []string{"id"}, []string{"order_id"}},
		{`WITH "Users" AS (SELECT 1 AS synthetic) SELECT u.| FROM users u`, []string{"id"}, []string{"synthetic"}},
		{`WITH "Users" AS (SELECT 1 AS synthetic) SELECT u.| FROM "Users" u`, []string{"synthetic"}, []string{"id"}},
		{`SELECT d.| FROM users u JOIN LATERAL (SELECT * FROM orders o) d ON true`, []string{"order_id"}, []string{"id"}},
		{`SELECT d.| FROM users u JOIN LATERAL (SELECT *) d ON true`, nil, []string{"id"}},
		{`SELECT d.| FROM (SELECT id, "display name" FROM users) d(user_id)`, []string{"user_id", "display name"}, []string{"id"}},
		{`SELECT d.| FROM (SELECT 1 AS n EXCEPT SELECT 2) d`, []string{"n"}, nil},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			items, _ := completionsAt(tc.sql, database.PostgreSQL, schemas, metadata)
			for _, want := range tc.want {
				if findCompletion(items, want) == nil {
					t.Fatalf("missing %q: %+v", want, items)
				}
			}
			for _, absent := range tc.absent {
				if findCompletion(items, absent) != nil {
					t.Fatalf("unexpected %q: %+v", absent, items)
				}
			}
		})
	}
}

func TestSQLInferenceMissingMetadata(t *testing.T) {
	schemas, _ := completionFixture()
	for _, sql := range []string{
		`WITH a AS (SELECT * FROM users) SELECT a.| FROM a`,
		`SELECT d.| FROM (SELECT u.* FROM users u) d`,
	} {
		_, missing := completionsAt(sql, database.PostgreSQL, schemas, nil)
		if len(missing) != 1 || missing[0].Name != "users" {
			t.Fatalf("wrong base-table metadata for %q: %+v", sql, missing)
		}
	}
	items, missing := completionsAt(`WITH users AS (SELECT 1 AS synthetic) SELECT users.| FROM users`, database.PostgreSQL, schemas, nil)
	if len(missing) != 0 || findCompletion(items, "synthetic") == nil {
		t.Fatalf("CTE fetched a shadowed table: %+v %+v", items, missing)
	}
}

func TestSQLInferenceBigQueryNestedProjection(t *testing.T) {
	table := database.Table{Project: "project", Schema: "analytics", Name: "events"}
	schemas := []database.Schema{{Project: table.Project, Name: table.Schema, Loaded: true, Tables: []database.Table{table}}}
	metadata := map[string]database.TableMetadata{table.ID(): {Columns: []database.Column{{Name: "payload", Type: "RECORD", Fields: []database.Column{{Name: "city", Type: "STRING"}}}}}}
	items, missing := completionsAt("WITH c AS (SELECT e.payload AS data FROM `project.analytics.events` e) SELECT c.data.| FROM c", database.BigQuery, schemas, metadata)
	if city := findCompletion(items, "city"); city == nil || city.Insert != "`city`" || len(missing) != 0 {
		t.Fatalf("nested field did not survive projection: %+v %+v", items, missing)
	}
}

func TestSQLInferenceBoundsAndIncompleteQueries(t *testing.T) {
	schemas, metadata := completionFixture()
	for _, sql := range []string{
		`WITH a AS (SELECT * FROM a) SELECT a.| FROM a`,
		`WITH RECURSIVE a AS (SELECT * FROM a) SELECT a.| FROM a`,
		`SELECT d.| FROM (SELECT id AS user_id FROM users`,
		`WITH a(x) AS (SELECT 1) SELECT a.| FROM a`,
		`WITH a AS (SELECT 1 AS n), broken AS (SELECT |`,
	} {
		items, missing := completionsAt(sql, database.PostgreSQL, schemas, metadata)
		if len(items) > completionLimit || len(missing) > 4 {
			t.Fatal("inference exceeded bounds")
		}
	}
	sql := "SELECT 1 AS n"
	for i := 0; i < inferenceDepthLimit+10; i++ {
		sql = "SELECT * FROM (" + sql + ") d"
	}
	sql = "SELECT d.| FROM (" + sql + ") d"
	items, _ := completionsAt(sql, database.PostgreSQL, schemas, metadata)
	if findCompletion(items, "n") != nil {
		t.Fatal("inference exceeded nesting bound")
	}
	for i := 0; i < 200; i++ {
		// Arbitrary truncation must be safe: the editor mostly sees incomplete SQL.
		text := `WITH a(x,y) AS (SELECT u.id, COUNT(*) FROM users u GROUP BY u.id), b AS (SELECT * FROM a) SELECT b.x FROM b`
		if i >= len(text) {
			break
		}
		c := sqlCompletionContext(text[:i], i, database.PostgreSQL)
		sqlCompletions(c, database.PostgreSQL, schemas, metadata)
	}
	if c := sqlCompletionContext(strings.Repeat("+", 32769), 32769, database.PostgreSQL); !c.blocked {
		t.Fatal("token budget not enforced")
	}
}
