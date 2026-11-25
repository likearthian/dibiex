package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func TestCredentialRetention(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows account encryption")
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	cfg := database.Config{Engine: database.PostgreSQL, Host: "localhost", Database: "app", User: "old_user"}
	key := credentialKey("Development", cfg)
	credential := connectionCredential{User: "new_user", Password: "private-database-password", Mode: passwordForever}
	change := credentialChange{key, credential}
	if err := writeCredentials(path, []credentialChange{change}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), credential.Password) || strings.Contains(string(raw), credential.User) {
		t.Fatal("plaintext credentials reached disk")
	}
	loaded, err := readCredentials(path)
	if err != nil || loaded[key] != credential {
		t.Fatalf("encrypted restart round trip: %v", err)
	}
	a := &App{}
	a.ensureWorkspace().credentials = loaded
	b := a.newConnectionSession()
	if got, ok := b.credential("Development", cfg); !ok || got != credential {
		t.Fatal("credentials are not shared across connection workspaces")
	}
	credential.Mode = passwordSession
	change.credential = credential
	if err := writeCredentials(path, []credentialChange{change}); err != nil {
		t.Fatal(err)
	}
	a.applyCredentials([]credentialChange{change})
	loaded, err = readCredentials(path)
	if err != nil || len(loaded) != 0 {
		t.Fatal("switching to session retention left a permanent password")
	}
	if got, ok := a.credential("Development", cfg); !ok || got.Password != credential.Password {
		t.Fatal("session credential was lost before restart")
	}
	a.cancelConnections()
	if _, ok := a.credential("Development", cfg); ok {
		t.Fatal("session credential survived shutdown")
	}
	a.applyCredentials([]credentialChange{{key, connectionCredential{Mode: passwordNever}}})
	if _, ok := a.credential("Development", cfg); ok {
		t.Fatal("Do not save retained a credential")
	}
	cfg.Host = "another-host"
	if credentialKey("Development", cfg) == key {
		t.Fatal("credentials are not scoped to their database endpoint")
	}
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCredentials(path, []credentialChange{change}); err != nil {
		t.Fatal(err)
	}
}

func TestSavedConnectionUsesCompactLogin(t *testing.T) {
	cfg := database.Config{Engine: database.PostgreSQL, Host: "localhost", Port: "5432", Database: "app", User: "postgres", SSLMode: "verify-full"}
	saved := savedConnection{Name: "Development", Config: cfg}
	a := &App{connections: []savedConnection{saved}, explorerWidth: 280, editorPercent: 60}
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Connect Development"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection || !a.connectionForm.credentialsOnly || a.connectionForm.config.Host != cfg.Host {
		t.Fatal("saved connection did not open its credential prompt")
	}
	for _, label := range []string{"User", "Password", "Save password"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing login field %s", label)
		}
	}
	for _, label := range []string{"Host", "Database engine", "Connection name", "SSL mode"} {
		if _, ok := v.Find(label); ok {
			t.Fatalf("full settings field %s leaked into login", label)
		}
	}
	a.connectionForm.config.Password = "temporary"
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.connectionForm.config.Password != "" {
		t.Fatal("cancelled login retained its password")
	}
	if err := v.Click("Actions for Development"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Reconnect"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection || !a.connectionForm.credentialsOnly {
		t.Fatal("Reconnect did not use compact login")
	}
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Actions for Development"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("Modify connection…"); err != nil {
		t.Fatal(err)
	}
	if a.connectionForm.credentialsOnly {
		t.Fatal("Modify must open full settings")
	}
	if _, ok := v.Find("Host"); !ok {
		t.Fatal("Modify did not open full settings")
	}
}

func TestSavedMySQLConnectionUsesCompactLogin(t *testing.T) {
	cfg := database.Config{Engine: database.MySQL, Host: "localhost", Port: "3306", Database: "shop", User: "root", SSLMode: "preferred"}
	a := &App{connections: []savedConnection{{Name: "Shop", Config: cfg}}, explorerWidth: 280, editorPercent: 60}
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Connect Shop"); err != nil {
		t.Fatal(err)
	}
	if !a.showConnection || !a.connectionForm.credentialsOnly || a.connectionForm.config.Host != cfg.Host {
		t.Fatal("saved MySQL connection did not open its credential prompt")
	}
	for _, label := range []string{"User", "Password", "Save password"} {
		if _, ok := v.Find(label); !ok {
			t.Fatalf("missing login field %s", label)
		}
	}
	for _, label := range []string{"Host", "Database engine", "Connection name", "TLS mode"} {
		if _, ok := v.Find(label); ok {
			t.Fatalf("full settings field %s leaked into login", label)
		}
	}
	if err := v.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.connectionForm.config.Password != "" || a.showConnection {
		t.Fatal("cancelled MySQL login retained its password")
	}
}

func TestDisconnectKeepsReconnectEntryAndDrafts(t *testing.T) {
	client, schemas := workspaceClient(t, "records")
	a := &App{explorerWidth: 280, editorPercent: 60}
	a.installConnection(client, nil, schemas, "Local", "")
	a.addQuery("SELECT 'keep this draft'")
	ctx := a.connectionContext()
	v := ui.NewTester(a.view, 1180, 780)
	if err := v.Click("Actions for Local"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Reconnect"); !ok {
		t.Fatal("active connection menu has no Reconnect")
	}
	if err := v.Click("Disconnect"); err != nil {
		t.Fatal(err)
	}
	<-a.closingConnection
	if ctx.Err() == nil || a.client != nil || a.querySQL() != "SELECT 'keep this draft'" {
		t.Fatal("Disconnect did not cancel its session and retain drafts")
	}
	if _, ok := v.Find("Connect Local"); !ok {
		t.Fatal("disconnected unsaved connection disappeared")
	}
	if err := v.Click("Actions for Local"); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Find("Reconnect"); !ok {
		t.Fatal("disconnected connection menu has no Reconnect")
	}
	newClient, err := database.Connect(a.connectionContext(), a.lastConnection.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	a.installConnection(newClient, nil, schemas, "Local", "")
	defer a.cancelConnections()
	if a.connectionContext().Err() != nil || a.querySQL() != "SELECT 'keep this draft'" {
		t.Fatal("reconnection discarded drafts or reused a cancelled context")
	}
}
