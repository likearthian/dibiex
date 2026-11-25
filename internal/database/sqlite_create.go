package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateSQLite creates an empty, initialized database and never replaces a file.
// Like Connect, this performs IO and must run on a worker.
func CreateSQLite(ctx context.Context, path string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("choose a filename for the new SQLite database")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("database path: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return fmt.Errorf("a file already exists at this path; use Browse to open it or choose a different name")
	}
	if err != nil {
		return fmt.Errorf("create SQLite file: %w", err)
	}
	created, statErr := file.Stat()
	defer func() {
		if err == nil || statErr != nil {
			return
		}
		// Only remove the file this call created, never a replacement at its path.
		if current, e := os.Stat(absolute); e == nil && os.SameFile(created, current) {
			_ = os.Remove(absolute)
		}
	}()
	if err = file.Close(); err != nil {
		return fmt.Errorf("close new SQLite file: %w", err)
	}
	if statErr != nil {
		return fmt.Errorf("inspect new SQLite file: %w", statErr)
	}
	client, err := Open(ctx, absolute)
	if err != nil {
		return err
	}
	// A write initializes the SQLite header without adding application tables.
	_, err = client.db.ExecContext(ctx, "PRAGMA user_version = 0")
	closeErr := client.Close()
	if err != nil {
		return fmt.Errorf("initialize SQLite database: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close SQLite database: %w", closeErr)
	}
	return nil
}
