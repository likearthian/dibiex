package app

import (
	"strconv"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) availableConnections() []savedConnection {
	connections := append([]savedConnection(nil), a.connections...)
	for _, session := range a.connectionSessions() {
		if session.lastConnection == nil {
			continue
		}
		found := false
		for _, saved := range connections {
			if saved.Name == session.lastConnection.Name {
				found = true
				break
			}
		}
		if !found {
			connections = append(connections, *session.lastConnection)
		}
	}
	return connections
}

func (a *App) requestConnection(saved savedConnection, reconnect bool) {
	if a.connectionStoreBusy || a.closed {
		return
	}
	target := a
	for _, session := range a.connectionSessions() {
		if session.workspaceName() == saved.Name {
			target = session
			break
		}
	}
	if target.hasConnection() {
		if target.connectionLabel() == saved.Name {
			if !reconnect {
				target.activateConnection()
				return
			}
			target.disconnectConnection()
		} else {
			target = target.newConnectionSession()
		}
	} else if target.busy {
		return
	}
	if target.hasBoundDrafts(saved.Name) {
		target = target.newConnectionSession()
	}
	target.activateConnection()
	cfg := saved.Config
	cfg.Password = ""
	credential, remembered := target.credential(saved.Name, cfg)
	mode := passwordSession
	if remembered {
		if cfg.Engine == database.PostgreSQL || cfg.Engine == database.MySQL {
			cfg.User, cfg.Password, mode = credential.User, credential.Password, credential.Mode
		}
	}
	target.connectionForm = connectionForm{name: saved.Name, originalName: saved.Name, engine: string(cfg.Engine), config: cfg, maxBytes: strconv.FormatInt(cfg.MaxBytesBilled, 10), passwordMode: mode}
	if (cfg.Engine == database.PostgreSQL || cfg.Engine == database.MySQL) && !remembered {
		target.connectionForm.credentialsOnly = true
		target.showConnection = true
		return
	}
	if err := cfg.Validate(); err != nil {
		target.status = "Connection failed: " + err.Error()
		target.pendingToasts = append(target.pendingToasts, ui.Toast{Title: "Connection failed", Description: err.Error(), Type: "error"})
		return
	}
	target.connect(cfg)
}

func passwordRetention(c *ui.Context, k palette, form *connectionForm, busy bool) {
	if form.passwordMode == "" {
		form.passwordMode = passwordSession
	}
	ui.Text(c, "Save password").FontSize(11).TextColor(k.muted)
	ui.Select(c.Key("password-retention"), &form.passwordMode, []string{passwordSession, passwordForever, passwordNever}).FillWidth().Disabled(busy).Label("Save password")
}

func (a *App) loginDialog(c *ui.Context, k palette) {
	form := &a.connectionForm
	wasOpen := a.showConnection
	ui.Modal(c.Key("connection-login"), &a.showConnection, func() {
		ui.Column(c).Width(360).Gap(12).Children(func() {
			ui.Text(c, "Sign in to "+form.name).FontSize(18).FontWeight(600).TextColor(k.text)
			connectionField(c, k, "User", &form.config.User, false)
			connectionField(c, k, "Password", &form.config.Password, true)
			passwordRetention(c, k, form, a.busy)
			if form.errorText != "" {
				ui.Text(c, form.errorText).MaxHeight(80).FontSize(11).TextColor(k.danger)
			}
			ui.Row(c).Gap(8).Children(func() {
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
	})
	if wasOpen && !a.showConnection {
		form.config.Password = ""
	}
}
