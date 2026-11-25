package database

import (
	"context"
	"testing"
	"time"
)

func TestQuerySessionsRunConcurrentlyAndKeepState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err = first.ExecuteResult(ctx, "CREATE TEMP TABLE private_state (id INTEGER); INSERT INTO private_state VALUES (7)"); err != nil {
		t.Fatal(err)
	}
	if _, err = second.Query(ctx, "SELECT * FROM private_state"); err == nil {
		t.Fatal("temporary state leaked between query tabs")
	}
	if result, err := second.Query(ctx, "PRAGMA foreign_keys"); err != nil || result.Rows[0][0] != "1" {
		t.Fatalf("new session lost SQLite foreign-key enforcement: %+v %v", result, err)
	}
	if _, err = first.ExecuteResult(ctx, "BEGIN; INSERT INTO private_state VALUES (8)"); err != nil {
		t.Fatal(err)
	}
	if _, err = second.Query(ctx, "SELECT 2"); err != nil {
		t.Fatal("another tab was blocked by an idle transaction", err)
	}
	if _, err = first.ExecuteResult(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if result, err := first.Query(ctx, "SELECT COUNT(*) FROM private_state"); err != nil || result.Rows[0][0] != "1" {
		t.Fatalf("tab transaction did not persist across executions: %+v %v", result, err)
	}
	slowCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := first.Query(slowCtx, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 1000000000) SELECT sum(x) FROM n`)
		done <- err
	}()
	<-started
	quickCtx, quickCancel := context.WithTimeout(ctx, time.Second)
	defer quickCancel()
	if result, err := second.Query(quickCtx, "SELECT 42"); err != nil || result.Rows[0][0] != "42" {
		stop()
		t.Fatalf("concurrent query did not complete: %+v %v", result, err)
	}
	stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("slow query was not cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop the selected query")
	}
	if _, err := second.Query(ctx, "SELECT 43"); err != nil {
		t.Fatal("cancelling one tab damaged another", err)
	}
	first.Close()
	third, err := client.NewQuerySession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if _, err = third.Query(ctx, "SELECT * FROM private_state"); err == nil {
		t.Fatal("closed tab session state was reused")
	}
	if result, err := client.Query(ctx, "SELECT COUNT(*) FROM projects"); err != nil || len(result.Rows) != 1 {
		t.Fatalf("closing a tab destroyed the shared in-memory database: %v", err)
	}
}

func TestQuerySessionBoundAndParentCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var sessions []*Client
	for range MaxQuerySessions {
		session, err := client.NewQuerySession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	if _, err = client.NewQuerySession(ctx); err == nil {
		t.Fatal("session limit was not enforced")
	}
	if _, err = client.Catalog(ctx); err != nil {
		t.Fatal("query sessions consumed the catalog connection", err)
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if _, err = session.Query(ctx, "SELECT 1"); err == nil {
			t.Fatal("disconnect left a query session usable")
		}
	}
	if _, err = client.NewQuerySession(ctx); err == nil {
		t.Fatal("closed parent allowed a new session")
	}
}
