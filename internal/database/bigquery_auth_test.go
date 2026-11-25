package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cloud.google.com/go/bigquery"
)

func TestBigQueryAuthSelection(t *testing.T) {
	ctx := context.Background()
	for _, method := range []string{"", "Existing ADC", BigQueryADC} {
		cfg := Config{BigQueryAuth: method}
		if opts, err := bigQueryAuthOptions(ctx, cfg); err != nil || len(opts) != 0 || cfg.BigQueryAuthMethod() != BigQueryADC {
			t.Fatalf("ADC method %q was not accepted: %v", method, err)
		}
	}
	for _, cfg := range []Config{
		{BigQueryAuth: "unknown"},
		{BigQueryAuth: "Browser sign-in"},
		{BigQueryAuth: BigQueryServiceAccount},
		{BigQueryAuth: BigQueryServiceAccount, CredentialFile: authTestFile(t, `{"type":"external_account"}`)},
		{BigQueryAuth: BigQueryServiceAccount, CredentialFile: authTestFile(t, `{"type":"service_account","token_uri":"https://untrusted.example/token"}`)},
	} {
		if _, err := bigQueryAuthOptions(ctx, cfg); err == nil {
			t.Fatalf("invalid auth accepted: %s", cfg.BigQueryAuth)
		}
	}
}

func authTestFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBigQueryServiceAccountProjectDetection(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		Engine: BigQuery, BigQueryAuth: BigQueryServiceAccount, MaxBytesBilled: 1024,
		CredentialFile: authTestFile(t, `{"type":"service_account","project_id":"key-project","client_email":"test@example.com","private_key":"fake-key","token_uri":"https://oauth2.googleapis.com/token"}`),
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("blank project should be accepted for a service account: %v", err)
	}
	opts, err := bigQueryAuthOptions(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	client, err := bigquery.NewClient(ctx, bigquery.DetectProjectID, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if got := client.Project(); got != "key-project" {
		t.Fatalf("detected %q, want key-project", got)
	}
}
