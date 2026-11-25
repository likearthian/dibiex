package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func cursorTestSession(t *testing.T) *Client {
	t.Helper()
	dsn := os.Getenv("DBCRAFT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DBCRAFT_TEST_POSTGRES_DSN for real PostgreSQL cursor tests")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test DSN")
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	root := &Client{db: stdlib.OpenDB(*config), config: Config{Engine: PostgreSQL}}
	t.Cleanup(func() { _ = root.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := root.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func assertCursorClosed(t *testing.T, p *PostgreSQLCursor) {
	t.Helper()
	select {
	case <-p.Done():
	default:
		t.Fatal("cursor still open")
	}
}

func TestPostgreSQLCursorBatchesAndTypes(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, first, err := session.StartPostgreSQLCursor(ctx, `SELECT i, NULL::text, decode('00ff','hex'), '{"a":1}'::jsonb FROM generate_series(1,2505) i`)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 1000 || !first.More || first.Rows[0][0] != "1" || first.Rows[999][0] != "1000" {
		t.Fatal("first batch was not bounded")
	}
	if first.Rows[0][1] != "NULL" || first.Rows[0][2] != "<BLOB 2 bytes>" || !strings.Contains(first.Rows[0][3], `"a"`) {
		t.Fatal("cursor lost NULL/BLOB/JSON presentation")
	}
	second, err := p.Fetch(ctx, 9000, maxResultBytes)
	if err != nil || len(second.Rows) != 1000 || second.Rows[0][0] != "1001" || second.Rows[999][0] != "2000" || !second.More {
		t.Fatalf("second batch lost rows: %v", err)
	}
	last, err := p.Fetch(ctx, 8000, maxResultBytes)
	if err != nil || len(last.Rows) != 505 || last.Rows[0][0] != "2001" || last.Rows[504][0] != "2505" || last.More {
		t.Fatalf("last batch: %v", err)
	}
	assertCursorClosed(t, p)
	if _, err := session.Query(ctx, "SELECT 42"); err != nil {
		t.Fatal("cursor transaction was not released", err)
	}
}

func TestPostgreSQLCursorDoesNotDrainMillionRows(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, result, err := session.StartPostgreSQLCursor(ctx, `SELECT i, pg_sleep(CASE WHEN i > 1001 THEN 0.01 ELSE 0 END) FROM generate_series(1,1000000) i`)
	if err != nil || len(result.Rows) != 1000 || !result.More {
		t.Fatalf("initial fetch: %v", err)
	}
	// Draining the remaining rows would take hours and exceed this deadline.
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	assertCursorClosed(t, p)
	if _, err := session.Query(ctx, "SELECT 1"); err != nil {
		t.Fatal("closing a cursor drained or damaged the session", err)
	}
}

func TestPostgreSQLCursorPreservesEditorTransactionAndRejectsScripts(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := session.ExecuteResult(ctx, "BEGIN; CREATE TEMP TABLE cursor_user_tx (id int); INSERT INTO cursor_user_tx VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.StartPostgreSQLCursor(ctx, "SELECT 1"); err == nil {
		t.Fatal("cursor took ownership of an editor transaction")
	}
	if result, err := session.Query(ctx, "SELECT * FROM cursor_user_tx"); err != nil || len(result.Rows) != 1 {
		t.Fatal("rejection rolled back the user transaction", err)
	}
	if _, err := session.ExecuteResult(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT 1; SELECT 2", "DELETE FROM pg_class", "WITH x AS (DELETE FROM pg_class RETURNING *) SELECT * FROM x"} {
		if _, _, err := session.StartPostgreSQLCursor(ctx, query); err == nil {
			t.Fatalf("cursor accepted script or write: %s", query)
		}
		if _, err := session.Query(ctx, "SELECT 1"); err != nil {
			t.Fatal("rejected query left a transaction open", err)
		}
	}
}

func TestPostgreSQLCursorBudgetsExpiryAndRerun(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, _, err := session.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := p.Fetch(ctx, 3, 100)
	if err != nil || len(batch.Rows) != 3 || !batch.Truncated || batch.More {
		t.Fatal("remaining row budget not enforced", err)
	}
	assertCursorClosed(t, p)
	p, _, err = session.StartPostgreSQLCursor(ctx, "SELECT repeat('x',100) FROM generate_series(1,3000)")
	if err != nil {
		t.Fatal(err)
	}
	batch, err = p.Fetch(ctx, 9000, 101)
	if err != nil || len(batch.Rows) != 1 || !batch.Truncated {
		t.Fatal("remaining byte budget not enforced", err)
	}
	assertCursorClosed(t, p)
	p, _, err = session.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.idleDeadline = time.Now().Add(-time.Second)
	p.mu.Unlock()
	p.expire()
	assertCursorClosed(t, p)
	p, _, err = session.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Query(ctx, "SELECT 7"); err != nil {
		t.Fatal(err)
	}
	assertCursorClosed(t, p)
}

func TestPostgreSQLCursorCancellationAndSessionClose(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, _, err := session.StartPostgreSQLCursor(ctx, `SELECT i, pg_sleep(CASE WHEN i > 1001 THEN 0.02 ELSE 0 END) FROM generate_series(1,1000000) i`)
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := p.Fetch(short, 9000, maxResultBytes); err == nil {
		t.Fatal("fetch ignored cancellation")
	}
	assertCursorClosed(t, p)
	other := cursorTestSession(t)
	p, _, err = other.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	assertCursorClosed(t, p)
}

func TestPostgreSQLCursorConnectionsRemainIndependent(t *testing.T) {
	first := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	second, err := first.parent.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := first.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := second.StartPostgreSQLCursor(ctx, "SELECT i FROM generate_series(1,3000) i")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertCursorClosed(t, p)
	batch, err := q.Fetch(ctx, 9000, maxResultBytes)
	if err != nil || len(batch.Rows) != 1000 || batch.Rows[0][0] != "1001" {
		t.Fatal("closing another tab damaged this cursor", err)
	}
}

func TestPostgreSQLTableBrowsingUsesServerPages(t *testing.T) {
	session := cursorTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := session.ExecuteResult(ctx, "CREATE TEMP TABLE cursor_page_test(id int PRIMARY KEY); INSERT INTO cursor_page_test SELECT generate_series(1,250)"); err != nil {
		t.Fatal(err)
	}
	namespace, err := session.Query(ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()")
	if err != nil {
		t.Fatal(err)
	}
	table := Table{Schema: namespace.Rows[0][0], Name: "cursor_page_test"}
	for page, want := range []int{100, 100, 50} {
		data, err := session.ReadPostgreSQLTablePage(ctx, table, 100, page*100, "", false)
		if err != nil || len(data.Rows) != want || data.Truncated != (page < 2) {
			t.Fatalf("page %d: %v", page, err)
		}
	}
}
