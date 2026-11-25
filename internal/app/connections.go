package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type connectionForm struct {
	name                string
	originalName        string
	engine              string
	config              database.Config
	maxBytes, errorText string
	infoText            string
	passwordMode        string
	credentialsOnly     bool
	adcChecked          bool
	gcloudPath          string
	adcSigningIn        bool
}

func (a *App) open() {
	if a.busy && a.hasConnection() {
		a.newConnectionSession().open()
		return
	}
	a.activateConnection()
	if a.busy || a.connectionStoreBusy {
		return
	}
	a.connectionForm = connectionForm{engine: string(database.PostgreSQL)}
	a.connectionForm.applyEngineDefaults()
	a.showConnection = true
}
func (a *App) pickSQLite() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.connectionDialogParent(), Title: "Open SQLite database", Filters: []mygo.FileFilter{
		{Name: "SQLite databases", Extensions: []string{"db", "sqlite", "sqlite3", "db3"}}, {Name: "All files", Extensions: []string{"*"}},
	}})
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	if len(paths) > 0 {
		a.connectionForm.config.Path = paths[0]
		a.connectionForm.errorText = ""
		a.connectionForm.infoText = ""
	}
}

func (a *App) newSQLite() {
	if a.busy || a.win == nil {
		return
	}
	defaultPath := "database.sqlite"
	if a.connectionForm.config.Path != "" {
		defaultPath = filepath.Join(filepath.Dir(a.connectionForm.config.Path), defaultPath)
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.connectionDialogParent(), Title: "Create SQLite database", ButtonLabel: "Create", DefaultPath: defaultPath, Filters: []mygo.FileFilter{{Name: "SQLite databases", Extensions: []string{"sqlite", "db", "sqlite3", "db3"}}}})
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	if path == "" {
		return
	}
	a.connectionForm.config.Path = path
	a.connectionForm.errorText = ""
	a.connectionForm.infoText = "Creating SQLite database…"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.busy = true
	a.cancel = cancel
	a.started = time.Time{}
	win := a.win
	go func() {
		defer cancel()
		err := database.CreateSQLite(ctx, path)
		win.Update(func() {
			if a.closed {
				return
			}
			a.busy = false
			a.cancel = nil
			if !a.showConnection || a.connectionForm.engine != string(database.SQLite) || a.connectionForm.config.Path != path {
				return
			}
			if err != nil {
				a.connectionForm.errorText = err.Error()
				a.connectionForm.infoText = ""
			} else {
				a.connectionForm.infoText = "Database created. Connect to use it."
			}
		})
	}()
}
func connectionField(c *ui.Context, k palette, label string, value *string, secret bool) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(k.muted)
		input := ui.TextInput(c.Key(label), value).FillWidth().Label(label)
		if secret {
			input.Password()
		}
	})
}

func (form *connectionForm) applyEngineDefaults() {
	engine := database.Engine(form.engine)
	form.config = database.Config{Engine: engine}
	form.errorText, form.infoText, form.passwordMode = "", "", ""
	form.adcChecked, form.gcloudPath = false, ""
	form.maxBytes = "1073741824"
	switch engine {
	case database.PostgreSQL:
		form.config.Host, form.config.Port = "localhost", "5432"
		form.config.Database, form.config.User, form.config.SSLMode = "postgres", "postgres", "verify-full"
	case database.MySQL:
		form.config.Host, form.config.Port = "localhost", "3306"
		form.config.User, form.config.SSLMode = "root", "preferred"
	}
}
func (a *App) connectionDialog(c *ui.Context, k palette) {
	if a.connectionForm.credentialsOnly {
		a.loginDialog(c, k)
		return
	}
	if a.win != nil {
		return
	}
	wasOpen := a.showConnection
	ui.Modal(c.Key("connection-dialog"), &a.showConnection, func() {
		a.connectionFormView(c, k, false)
	})
	if wasOpen && !a.showConnection {
		a.clearConnectionDialogSecrets()
	}
}

func (a *App) connectionDialogParent() *mygo.Window {
	if a.connectionWindow != nil {
		return a.connectionWindow
	}
	return a.win
}

func (a *App) clearConnectionDialogSecrets() {
	a.connectionForm.config.Password = ""
}

func (a *App) syncConnectionWindow() {
	if a.win == nil {
		return
	}
	for _, owner := range a.connectionSessions() {
		if owner.connectionWindow != nil && (!owner.showConnection || owner.connectionForm.credentialsOnly || owner != a) {
			window := owner.connectionWindow
			owner.connectionWindow = nil
			owner.clearConnectionDialogSecrets()
			window.Destroy()
		}
	}
	if !a.showConnection || a.connectionForm.credentialsOnly {
		return
	}
	if a.connectionWindow != nil {
		a.connectionWindow.Update(func() {})
		return
	}
	window := newModalWindow(mygo.WindowOptions{
		Title: "Connection settings", Width: 570, Height: 734, MinWidth: 460, MinHeight: 464,
		Parent: a.win,
	}, func(c *ui.Context) {
		if !a.showConnection || a.connectionForm.credentialsOnly {
			return
		}
		a.connectionFormView(c, paletteOf(c), true)
		if !a.showConnection {
			a.win.Update(func() {})
		}
	})
	a.connectionWindow = window
	window.OnClose(func(e *mygo.CloseEvent) {
		if a.busy || a.connectionStoreBusy {
			e.PreventDefault()
		}
	})
	window.OnClosed(func() {
		if a.connectionWindow == window {
			a.connectionWindow = nil
			a.showConnection = false
			a.clearConnectionDialogSecrets()
			a.win.Update(func() {})
		}
	})
}

func (a *App) connectionFormView(c *ui.Context, k palette, native bool) {
	_, height := c.Size()
	form := &a.connectionForm
	panel := ui.Column(c).Gap(10)
	if native {
		panel.Fill().Padding(20)
	} else {
		panel.Width(460)
	}
	panel.Children(func() {
		ui.Text(c, "Connect to database").FontSize(18).FontWeight(600).TextColor(k.text)
		scroll := ui.Scroll(c)
		if native {
			scroll.Grow(1).MinHeight(0)
		} else {
			scroll.MaxHeight(max(float32(160), height-240))
		}
		scroll.Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				connectionField(c, k, "Connection name", &form.name, false)
				if ui.Select(c.Key("engine"), &form.engine, []string{string(database.SQLite), string(database.PostgreSQL), string(database.MySQL), string(database.BigQuery)}).FillWidth().Disabled(a.busy).Label("Database engine").Changed() {
					form.applyEngineDefaults()
				}
				switch database.Engine(form.engine) {
				case database.SQLite:
					connectionField(c, k, "Database file", &form.config.Path, false)
					ui.Row(c).Gap(8).Children(func() {
						if ui.Button(c, "Browse…").Disabled(a.busy).Clicked() {
							a.pickSQLite()
						}
						if ui.Button(c, "New file…").Disabled(a.busy).Clicked() {
							a.newSQLite()
						}
					})
					ui.Text(c, "Browse for an existing database, or create an empty SQLite file.").FontSize(11).TextColor(k.muted)
				case database.PostgreSQL:
					connectionField(c, k, "Host", &form.config.Host, false)
					ui.Row(c).Gap(12).Children(func() {
						ui.Column(c).Grow(1).Children(func() { connectionField(c, k, "Database", &form.config.Database, false) })
						ui.Column(c).Width(90).Children(func() { connectionField(c, k, "Port", &form.config.Port, false) })
					})
					connectionField(c, k, "User", &form.config.User, false)
					connectionField(c, k, "Password", &form.config.Password, true)
					passwordRetention(c, k, form, a.busy)
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "SSL mode").TextColor(k.muted)
						ui.Select(c.Key("ssl-mode"), &form.config.SSLMode, []string{"verify-full", "verify-ca", "require", "prefer", "disable"}).Grow(1).Label("SSL mode")
					})
				case database.MySQL:
					connectionField(c, k, "Host", &form.config.Host, false)
					ui.Row(c).Gap(12).Children(func() {
						ui.Column(c).Grow(1).Children(func() { connectionField(c, k, "Database", &form.config.Database, false) })
						ui.Column(c).Width(90).Children(func() { connectionField(c, k, "Port", &form.config.Port, false) })
					})
					connectionField(c, k, "User", &form.config.User, false)
					connectionField(c, k, "Password", &form.config.Password, true)
					passwordRetention(c, k, form, a.busy)
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "TLS mode").TextColor(k.muted)
						ui.Select(c.Key("mysql-tls"), &form.config.SSLMode, []string{"preferred", "true", "skip-verify", "false"}).Grow(1).Label("TLS mode")
					})
					ui.Text(c, "preferred negotiates TLS when the server offers it; true verifies the server certificate and hostname.").FontSize(11).TextColor(k.muted)
				case database.BigQuery:
					a.bigQueryAuthentication(c, k)
					connectionField(c, k, "Billing project ID (auto-detect if blank)", &form.config.Project, false)
					connectionField(c, k, "Dataset project ID (optional)", &form.config.DatasetProject, false)
					if len(form.config.AdditionalProjects) > 0 {
						ui.Text(c, "Additional projects: "+strings.Join(form.config.AdditionalProjects, ", ")).FontSize(11).TextColor(k.muted)
					}
					connectionField(c, k, "Default dataset ID (optional)", &form.config.Dataset, false)
					connectionField(c, k, "Location (optional, e.g. US or asia-southeast2)", &form.config.Location, false)
					connectionField(c, k, "Maximum bytes billed per query", &form.maxBytes, false)
					ui.Text(c, "Queries may incur Google Cloud charges. The default billing cap is 1 GiB per query.").FontSize(11).TextColor(k.muted)
				}
			})
		})
		if form.errorText != "" {
			ui.Text(c, form.errorText).MaxHeight(65).FontSize(11).TextColor(k.danger)
		}
		if form.infoText != "" {
			ui.Text(c, form.infoText).FontSize(11).TextColor(k.green)
		}
		ui.Text(c, "Connection settings are saved locally. Forever passwords use Windows account encryption.").FontSize(11).TextColor(k.muted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "Test").Disabled(a.busy).Clicked() {
				a.testConnection()
			}
			if ui.Button(c, "Save connection").Disabled(a.busy || a.connectionStoreBusy).Clicked() {
				a.saveConnection()
			}
			ui.Spacer(c)
			if ui.Button(c, "Cancel").Disabled(a.busy).Clicked() {
				a.showConnection = false
			}
			if ui.PrimaryButton(c, "Connect").Disabled(a.busy || a.connectionStoreBusy).Clicked() {
				cfg, err := a.formConfig()
				if err != nil {
					form.errorText = err.Error()
					return
				}
				a.connect(cfg)
			}
		})
	})
}

func (a *App) connect(cfg database.Config) {
	if a.busy || a.closed || a.connectionStoreBusy {
		return
	}
	name := strings.TrimSpace(a.connectionForm.name)
	if name == "" {
		name = cfg.Label()
	}
	for _, session := range a.connectionSessions() {
		if session.client != nil && session.connectionLabel() == name {
			if !sameConnection(session.client.Settings(), cfg) {
				a.connectionForm.errorText = "Disconnect the existing connection before changing its settings, or use another name."
				return
			}
			a.showConnection = false
			a.connectionForm.config.Password = ""
			session.activateConnection()
			return
		}
	}
	form := a.connectionForm
	form.config.Password = ""
	mode := form.passwordMode
	if mode == "" {
		mode = passwordSession
	}
	credential := connectionCredential{User: cfg.User, Password: cfg.Password, Mode: mode}
	cleanConfig := cfg
	cleanConfig.Password = ""
	form.name, form.engine, form.config = name, string(cfg.Engine), cleanConfig
	connections, configErr := upsertConnection(a.connections, name, savedConnection{Name: name, Config: cleanConfig})
	if configErr != nil {
		a.connectionForm.errorText = configErr.Error()
		return
	}
	changes := []credentialChange{}
	if cfg.Engine == database.PostgreSQL || cfg.Engine == database.MySQL {
		changes = append(changes, credentialChange{credentialKey(name, cfg), credential})
	}
	a.showConnection = false
	a.connectionForm.config.Password = ""
	for _, owner := range a.connectionSessions() {
		if owner != a && !owner.hasConnection() && !owner.busy && owner.queryWorkspace == name {
			a = owner
			break
		}
	}
	if a.hasConnection() || a.hasBoundDrafts(name) {
		a = a.newConnectionSession()
	}
	a.activateConnection()
	a.connectionForm = form
	a.lastConnection = &savedConnection{Name: name, Config: cleanConfig}
	a.setConnectionStoreBusy(true)
	closing := a.closingConnection
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.busy, a.cancel, a.status, a.errorText = true, cancel, "Connecting to "+string(cfg.Engine)+"…", ""
	a.connecting = true
	a.started = time.Time{}
	win := a.win
	go func() {
		defer cancel()
		var client *database.Client
		var err error
		if closing != nil {
			select {
			case <-closing:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			client, err = database.Connect(ctx, cfg)
		}
		if err == nil && cfg.Engine == database.BigQuery {
			cleanConfig.Project = client.Settings().Project
			connections, err = upsertConnection(connections, name, savedConnection{Name: name, Config: cleanConfig})
		}
		cfg.Password = ""
		var schemas []database.Schema
		var pipeline *database.Introspector
		var cachedTables map[string]database.TableMetadata
		cacheWarning := ""
		if err == nil {
			cacheDir, cacheErr := database.DefaultSchemaCacheDir()
			pipeline = database.NewIntrospector(client, cacheDir)
			schemas, err = pipeline.Catalog(ctx, false)
			cachedTables = pipeline.CachedTables()
			cacheWarning = pipeline.Warning()
			if cacheErr != nil {
				cacheWarning = "Schema cache unavailable: " + cacheErr.Error()
			}
		}
		if err == nil {
			path, pathErr := connectionPath()
			err = pathErr
			if err == nil {
				err = writeConnections(path, connections)
			}
			if err == nil && len(changes) > 0 {
				path, err = credentialPath()
				if err == nil {
					err = writeCredentials(path, changes)
				}
			}
			if err != nil {
				err = fmt.Errorf("save connection credentials: %w", err)
			}
		}
		if err != nil && client != nil {
			client.Close()
			client = nil
		}
		applied := false
		mygo.RunOnMain(func() {
			if a.closed {
				return
			}
			a.busy, a.cancel = false, nil
			a.connecting = false
			a.setConnectionStoreBusy(false)
			if err != nil {
				a.errorText, a.status = err.Error(), "Connection failed"
				a.output, a.hideResults = true, false
				if cfg.Engine == database.PostgreSQL || cfg.Engine == database.MySQL {
					a.connectionForm.credentialsOnly = true
					a.connectionForm.config = cleanConfig
					a.connectionForm.errorText = err.Error()
					a.showConnection = true
				}
				if cfg.Engine == database.BigQuery {
					a.connectionForm.config = cleanConfig
					a.connectionForm.errorText = err.Error()
					a.showConnection = true
				}
				return
			}
			a.setSavedConnections(connections)
			a.applyCredentials(changes)
			target := a.installConnection(client, pipeline, schemas, name, cacheWarning)
			target.metadata = cachedTables
			target.loadExplorerCatalog(false)
			inspectionCtx := target.connectionContext()
			go func() {
				ctx, cancel := context.WithTimeout(inspectionCtx, 2*time.Minute)
				defer cancel()
				pipeline.Inspect(ctx, schemas)
			}()
			applied = true
		})
		if client != nil && !applied {
			client.Close()
		}
		win.Update(func() {})
	}()
}
