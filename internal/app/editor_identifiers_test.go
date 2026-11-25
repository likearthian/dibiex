package app

import (
	"strings"
	"testing"

	"dbcraft/internal/database"
)

func TestMySQLCompletionAlwaysQuotesIdentifiers(t *testing.T) {
	for _, name := range []string{"users", "Users", "user ID", "order", "a`b", ""} {
		got := completionIdentifier(name, database.MySQL)
		if !strings.HasPrefix(got, "`") || !strings.HasSuffix(got, "`") {
			t.Fatalf("%q was not backticked: %q", name, got)
		}
	}
	if got, want := completionIdentifier("a`b", database.MySQL), "`a``b`"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := completionTableReference(database.Table{Schema: "shop", Name: "order"}, database.MySQL), "`shop`.`order`"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPostgreSQLCompletionQuotesOnlyWhenNeeded(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"users", "users"}, {"public", "public"}, {"user_id", "user_id"}, {"_temp2", "_temp2"}, {"value$1", "value$1"},
		{"Users", `"Users"`}, {"user ID", `"user ID"`}, {"1st", `"1st"`}, {"$value", `"$value"`},
		{"user", `"user"`}, {"select", `"select"`}, {"order", `"order"`}, {"join", `"join"`}, {"authorization", `"authorization"`},
		{"a.b", `"a.b"`}, {`a"b`, `"a""b"`}, {"a;b", `"a;b"`}, {"", `""`},
	} {
		if got := completionIdentifier(tc.name, database.PostgreSQL); got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		table database.Table
		want  string
	}{
		{database.Table{Schema: "public", Name: "users"}, "public.users"},
		{database.Table{Schema: "public", Name: "Users"}, `public."Users"`},
		{database.Table{Schema: "App Data", Name: "users"}, `"App Data".users`},
		{database.Table{Schema: "public", Name: "order"}, `public."order"`},
		{database.Table{Name: "users"}, "users"},
	} {
		if got := completionTableReference(tc.table, database.PostgreSQL); got != tc.want {
			t.Fatalf("%+v: got %q, want %q", tc.table, got, tc.want)
		}
	}
}
