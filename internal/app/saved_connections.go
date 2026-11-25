package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dbcraft/internal/database"
)

type savedConnection struct {
	Name   string
	Config database.Config
}

func connectionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DBCraft", "connections.json"), nil
}

func readConnections(path string) ([]savedConnection, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var connections []savedConnection
	err = json.Unmarshal(data, &connections)
	for i := range connections {
		connections[i].Config.Password = ""
	}
	return connections, err
}

func writeConnections(path string, connections []savedConnection) error {
	clean := append([]savedConnection(nil), connections...)
	for i := range clean {
		clean[i].Config.Password = ""
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "connections-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (a *App) loadConnections() {
	a.setConnectionStoreBusy(true)
	go func() {
		path, err := connectionPath()
		var connections []savedConnection
		credentials := map[string]connectionCredential{}
		var credentialErr error
		if err == nil {
			connections, err = readConnections(path)
			if credentialFile, pathErr := credentialPath(); pathErr == nil {
				credentials, credentialErr = readCredentials(credentialFile)
			} else {
				credentialErr = pathErr
			}
		}
		a.win.Update(func() {
			if a.closed {
				return
			}
			a.setConnectionStoreBusy(false)
			if err != nil {
				a.status = "Could not load saved connections: " + err.Error()
			} else {
				a.setSavedConnections(connections)
				a.ensureWorkspace().credentials = credentials
				if credentialErr != nil {
					a.status = "Saved passwords unavailable; sign in to connect"
				}
			}
		})
	}()
}

func (a *App) saveConnections(connections []savedConnection, after ...func()) {
	a.saveConnectionsWithCredentials(connections, nil, after...)
}

func (a *App) saveConnectionsWithCredentials(connections []savedConnection, changes []credentialChange, after ...func()) {
	if a.connectionStoreBusy {
		return
	}
	a.setConnectionStoreBusy(true)
	go func() {
		path, err := connectionPath()
		if err == nil {
			err = writeConnections(path, connections)
		}
		if err == nil && len(changes) > 0 {
			var credentialFile string
			credentialFile, err = credentialPath()
			if err == nil {
				err = writeCredentials(credentialFile, changes)
			}
		}
		a.win.Update(func() {
			if a.closed {
				return
			}
			a.setConnectionStoreBusy(false)
			if err != nil {
				a.connectionForm.errorText = err.Error()
				a.status = "Could not save connections: " + err.Error()
				return
			}
			a.setSavedConnections(connections)
			a.applyCredentials(changes)
			a.status = "Connections saved locally"
			for _, apply := range after {
				apply()
			}
		})
	}()
}

func (a *App) formConfig() (database.Config, error) {
	f := a.connectionForm
	cfg := f.config
	cfg.Engine = database.Engine(f.engine)
	if cfg.Engine == database.BigQuery {
		n, err := strconv.ParseInt(f.maxBytes, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("enter a whole number for maximum bytes billed")
		}
		cfg.MaxBytesBilled = n
	}
	return cfg, cfg.Validate()
}

func (a *App) saveConnection() {
	cfg, err := a.formConfig()
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	password := cfg.Password
	cfg.Password = ""
	name := strings.TrimSpace(a.connectionForm.name)
	if name == "" {
		name = cfg.Label()
	}
	original := a.connectionForm.originalName
	next := savedConnection{name, cfg}
	mode := a.connectionForm.passwordMode
	if mode == "" {
		mode = passwordSession
	}
	changes := []credentialChange{}
	if cfg.Engine == database.PostgreSQL || cfg.Engine == database.MySQL {
		credential := connectionCredential{User: cfg.User, Password: password, Mode: mode}
		remembered := false
		// Editing settings without entering a replacement keeps the remembered password.
		if password == "" {
			if previous, ok := a.credential(original, cfg); ok {
				credential.Password = previous.Password
				remembered = true
			}
		}
		if password == "" && !remembered {
			credential.Mode = passwordNever
		}
		changes = append(changes, credentialChange{credentialKey(name, cfg), credential})
	}
	for _, saved := range a.connections {
		if saved.Name == original && credentialKey(saved.Name, saved.Config) != credentialKey(name, cfg) {
			changes = append(changes, credentialChange{key: credentialKey(saved.Name, saved.Config), credential: connectionCredential{Mode: passwordNever}})
		}
	}
	connections, err := upsertConnection(a.connections, original, next)
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	target := a
	for _, session := range a.connectionSessions() {
		if session.hasConnection() && (session.connectionLabel() == original || (original == "" && sameConnection(session.client.Settings(), cfg))) {
			target = session
			break
		}
	}
	expected := target.client
	active := target.hasConnection() && (target.connectionLabel() == original || (original == "" && sameConnection(target.client.Settings(), cfg)))
	a.saveConnectionsWithCredentials(connections, changes, func() {
		if active && target.client == expected {
			if sameConnection(target.client.Settings(), cfg) {
				target.connection = name
			} else {
				target.disconnectConnection()
				target.status = "Connection saved · connect to use the updated settings"
			}
		}
		if a.connectionForm.name == name {
			a.connectionForm.originalName = name
		}
		for _, session := range a.connectionSessions() {
			if session.lastConnection != nil && session.lastConnection.Name == original {
				session.lastConnection = &savedConnection{Name: name, Config: cfg}
			}
		}
	})
}

func upsertConnection(existing []savedConnection, original string, next savedConnection) ([]savedConnection, error) {
	index := -1
	for i, saved := range existing {
		if saved.Name == original && original != "" {
			index = i
			break
		}
	}
	for i, saved := range existing {
		if saved.Name == next.Name {
			if (index >= 0 && index != i) || (original != "" && original != next.Name) {
				return nil, fmt.Errorf("a connection named %q already exists", next.Name)
			}
			index = i
			break
		}
	}
	result := append([]savedConnection(nil), existing...)
	if index < 0 {
		return append(result, next), nil
	}
	result[index] = next
	return result, nil
}

func (a *App) testConnection() {
	cfg, err := a.formConfig()
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.busy, a.cancel = true, cancel
	a.started = time.Time{}
	a.connectionForm.errorText = "Testing connection…"
	go func() {
		defer cancel()
		client, err := database.Connect(ctx, cfg)
		if client != nil {
			client.Close()
		}
		a.win.Update(func() {
			if a.closed {
				return
			}
			a.busy, a.cancel = false, nil
			if err != nil {
				a.connectionForm.errorText = err.Error()
				a.connectionForm.infoText = ""
			} else {
				a.connectionForm.errorText = ""
				a.connectionForm.infoText = "Connection successful"
			}
		})
	}()
}
