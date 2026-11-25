package app

import (
	"strings"
	"testing"

	"dbcraft/internal/database"
)

func TestSavedBigQueryBrowserConnectionFailsValidation(t *testing.T) {
	cfg := database.Config{Engine: database.BigQuery, Project: "billing", MaxBytesBilled: 1 << 30, BigQueryAuth: "Browser sign-in"}
	saved := savedConnection{Name: "Warehouse", Config: cfg}
	a := &App{connections: []savedConnection{saved}}
	a.requestConnection(saved, false)
	if a.showConnection || a.busy || !strings.Contains(a.status, "unsupported BigQuery authentication method") {
		t.Fatalf("unsupported connection was not rejected: %q", a.status)
	}
}
