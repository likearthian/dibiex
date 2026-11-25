package database

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSQLiteDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "new database #世界.sqlite")
	if err := CreateSQLite(ctx, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		t.Fatal("new file is not an initialized SQLite database")
	}
	client, err := Connect(ctx, Config{Engine: SQLite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := client.Catalog(ctx)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if len(schemas) != 1 || len(schemas[0].Tables) != 0 {
		client.Close()
		t.Fatal("new database must be empty, without demo tables")
	}
	if _, err = client.Execute(ctx, "CREATE TABLE notes(body TEXT); INSERT INTO notes VALUES ('persisted');"); err != nil {
		client.Close()
		t.Fatal(err)
	}
	client.Close()
	reopened, err := Connect(ctx, Config{Engine: SQLite, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, err := reopened.Query(ctx, "SELECT body FROM notes")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "persisted" {
		t.Fatalf("new database did not persist data: %+v %v", result, err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = CreateSQLite(ctx, path); err == nil {
		t.Fatal("creation replaced an existing database")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("existing database was changed")
	}
}

func TestCreateSQLiteFailures(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(dir, "cancelled.sqlite")
	if err := CreateSQLite(ctx, path); err == nil {
		t.Fatal("cancelled creation succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled creation left a file")
	}
	if err := CreateSQLite(context.Background(), filepath.Join(dir, "missing", "new.sqlite")); err == nil {
		t.Fatal("missing directory was accepted")
	}
	if err := CreateSQLite(context.Background(), dir); err == nil {
		t.Fatal("creation replaced a directory")
	}
	if err := CreateSQLite(context.Background(), ""); err == nil {
		t.Fatal("empty filename created a database")
	}
}
