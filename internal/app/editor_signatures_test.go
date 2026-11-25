package app

import (
	"strings"
	"testing"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestSQLFunctionArgumentHelp(t *testing.T) {
	for _, tc := range []struct {
		sql, name string
		argument  int
		engine    database.Engine
	}{
		{`SELECT COALESCE(|`, "COALESCE", 0, database.SQLite},
		{`SELECT COALESCE(NULL, |`, "COALESCE", 1, database.SQLite},
		{`SELECT COALESCE(LOWER('x,y'), |`, "COALESCE", 1, database.PostgreSQL},
		{`SELECT COALESCE(LOWER(|`, "LOWER", 0, database.PostgreSQL},
		{`SELECT REPLACE('a,b', 'a', |`, "REPLACE", 2, database.SQLite},
		{`SELECT REPLACE('a,b', 'a|`, "REPLACE", 1, database.SQLite},
		{`SELECT CAST(id AS |`, "CAST", 1, database.PostgreSQL},
		{`SELECT SAFE_CAST(id AS |`, "SAFE_CAST", 1, database.BigQuery},
		{`SELECT COALESCE(ARRAY[1,2], |`, "COALESCE", 1, database.BigQuery},
		{`SELECT COALESCE((1 + 2), |`, "COALESCE", 1, database.PostgreSQL},
		{`SELECT REPLACE(/* comma, */ 'x', |`, "REPLACE", 1, database.PostgreSQL},
		{`SELECT REPLACE($text$x,y$text$, |`, "REPLACE", 1, database.PostgreSQL},
		{`SELECT STRING_AGG(value, |`, "STRING_AGG", 1, database.PostgreSQL},
		{`SELECT STRING_AGG(value, |`, "STRING_AGG", 1, database.BigQuery},
		{`SELECT REPLACE('x', 'y', 'z')|`, "", 0, database.SQLite},
		{`SELECT UNKNOWN_FN(|`, "", 0, database.SQLite},
		{`SELECT app.REPLACE(|`, "", 0, database.PostgreSQL},
		{`SELECT SAFE_CAST(x AS |`, "", 0, database.SQLite},
		{`SELECT GROUP_CONCAT(x, |`, "GROUP_CONCAT", 1, database.SQLite},
		{`SELECT GROUP_CONCAT(x, |`, "GROUP_CONCAT", 1, database.MySQL},
		{`SELECT IFNULL(CONCAT('a','b'), |`, "IFNULL", 1, database.MySQL},
		{`SELECT DATE_FORMAT(NOW(), |`, "DATE_FORMAT", 1, database.MySQL},
		{`SELECT COALESCE(NULL, NULL, NULL, |`, "COALESCE", 3, database.SQLite},
	} {
		t.Run(tc.sql+string(tc.engine), func(t *testing.T) {
			offset := strings.IndexByte(tc.sql, '|')
			help := functionArgumentHelp(strings.Replace(tc.sql, "|", "", 1), offset, tc.engine)
			if tc.name == "" {
				if help != nil {
					t.Fatalf("unexpected signature: %+v", help)
				}
				return
			}
			if help == nil || help.signature.name != tc.name || help.argument != tc.argument {
				t.Fatalf("wanted %s argument %d, got %+v", tc.name, tc.argument, help)
			}
		})
	}
}

func TestSQLFunctionHelpNativeView(t *testing.T) {
	a := &App{}
	a.addQuery("")
	v := ui.NewTester(func(c *ui.Context) { a.sqlEditor(c) }, 800, 400)
	if err := v.Click("SQL query editor"); err != nil {
		t.Fatal(err)
	}
	v.Type("SELECT COALESCE(")
	if _, ok := v.Find("SQL function arguments"); !ok {
		t.Fatalf("missing native argument helper: %v", v.Texts())
	}
	if !v.HasText("Argument 1 · Returns the first non-NULL value.") {
		t.Fatalf("wrong first argument: %v", v.Texts())
	}
	v.Type("NULL, ")
	if !v.HasText("Argument 2 · Returns the first non-NULL value.") {
		t.Fatalf("argument did not advance: %v", v.Texts())
	}
	v.Key(0, ui.KeyEscape)
	if _, ok := v.Find("SQL function arguments"); ok {
		t.Fatal("Escape did not dismiss argument helper")
	}
	v.Type("1)")
	if _, ok := v.Find("SQL function arguments"); ok {
		t.Fatal("closed call left an argument helper")
	}
}
