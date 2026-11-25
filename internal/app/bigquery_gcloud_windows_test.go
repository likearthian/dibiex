package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGoogleCloudADCLauncher(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Cloud SDK & tools")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "gcloud.cmd")
	script := "@echo off\r\nif not \"%*\"==\"auth application-default login\" exit /b 23\r\nexit /b 0\r\n"
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if found := findGoogleCloudCLI(); found != path {
		t.Fatalf("found %q, want %q", found, path)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := loginGoogleCloudADC(ctx, path); err != nil {
		t.Fatal(err)
	}
}
