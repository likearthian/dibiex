package app

import (
	"context"
	"fmt"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) hasConnection() bool { return a.client != nil || a.connection != "" }

func (a *App) connectionContext() context.Context {
	if a.sessionContext != nil {
		return a.sessionContext
	}
	return context.Background()
}

func (a *App) connectionLabel() string {
	if saved, ok := a.matchingConnection(); ok && a.hasConnection() {
		return saved.Name
	}
	return a.connection
}

func (a *App) engineLabel() string {
	if !a.hasConnection() {
		return "No connection"
	}
	return string(a.client.Engine())
}

func (a *App) connectionConnecting(name string) bool {
	for _, session := range a.connectionSessions() {
		if session.connecting && session.lastConnection != nil && session.lastConnection.Name == name {
			return true
		}
	}
	return false
}

func (a *App) connectionRoots(c *ui.Context, k palette) {
	connected := map[string]bool{}
	for i, session := range a.connectionSessions() {
		if !session.hasConnection() {
			continue
		}
		connected[session.connectionLabel()] = true
		if session.treeClosed == nil {
			session.treeClosed = map[string]bool{}
		}
		root := ui.Column(c.Key(fmt.Sprintf("connection-session:%d", i))).FillWidth().MinWidth(0)
		root.Children(func() { session.activeConnectionTree(c, k) })
		if root.Clicked() {
			session.activateConnection()
		}
	}
	for _, saved := range a.availableConnections() {
		if connected[saved.Name] {
			continue
		}
		connecting := a.connectionConnecting(saved.Name)
		hint := saved.Name + " · Not connected · Click to connect"
		if connecting {
			hint = saved.Name + " · Connecting…"
		}
		row := ui.Row(c.Key("saved-connection:"+saved.Name)).FillWidth().MinWidth(0).Height(28).Padding(0, 6, 0, 8).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Connect " + saved.Name).Tooltip(hint)
		if row.Hovered() {
			row.Background(k.border)
		}
		row.Children(func() {
			if connecting {
				ui.Spinner(c).Size(12, 12).Shrink(0).TextColor(k.accent).Label("Connecting " + saved.Name)
			} else {
				ui.Icon(c, controlIcon("chevron-right")).Size(12, 12).TextColor(k.muted)
			}
			databaseIcon(c, k, saved.Config.Engine)
			ui.Text(c, saved.Name).SingleLine().Grow(1).MinWidth(0).TextColor(k.muted)
			objectActions(c, k, row, saved.Name, func(m *ui.Menu) { a.savedConnectionMenu(m, saved) })
		})
		if row.Clicked() && !a.connectionStoreBusy {
			a.requestConnection(saved, false)
		}
	}
	if len(connected) == 0 && len(a.availableConnections()) == 0 {
		ui.Text(c, "No connections").Margin(12, 8).FontSize(12).TextColor(k.muted)
		ui.Text(c, "Use + to add a connection.").Margin(0, 8).FontSize(11).TextColor(k.muted)
	}
}

func (a *App) disconnectConnection() <-chan error {
	a.catalogPicker = catalogPicker{}
	a.showBigQueryProject = false
	a.connecting = false
	a.tableMenuState = tableMenuState{}
	if a.tableAction != nil && a.tableAction.cancel != nil {
		a.tableAction.cancel()
	}
	if a.tableData != nil && a.tableData.cancel != nil {
		a.tableData.cancel()
	}
	a.showTableAction, a.showTableData, a.showTableERD = false, false, false
	a.tableAction, a.tableData, a.tableERD = nil, nil, nil
	a.cancelTabQueries()
	a.showCreateTable, a.tableDesigner = false, nil
	a.sqliteDesigner = nil
	if a.client != nil {
		a.lastConnection = &savedConnection{Name: a.connectionLabel(), Config: a.connectionSettings()}
	}
	old := a.client
	if a.cancel != nil {
		a.cancel()
	}
	if a.sessionCancel != nil {
		a.sessionCancel()
	}
	a.sessionContext, a.sessionCancel = nil, nil
	a.client, a.introspector = nil, nil
	a.cancel, a.busy = nil, false
	a.connection, a.activeTable, a.filter, a.cacheWarning, a.errorText = "", "", "", "", ""
	a.selectedTree = ""
	a.explorerSearch = explorerSchemaSearch{}
	a.explorerCatalog = database.ExplorerCatalog{}
	a.explorerLoading = false
	a.explorerError = ""
	a.inspectorObject = nil
	a.inspectorColumn = nil
	a.inspectorDefinition = nil
	a.objectPath = nil
	a.schemas = nil
	a.inspectorTable = database.Table{}
	a.invalidateMetadata()
	a.treeClosed, a.fieldOpen, a.schemaErrors, a.schemaLoading = nil, nil, nil, nil
	a.tableSectionsOpen, a.propertyOpen = nil, nil
	a.result, a.grid, a.selected, a.page = database.Result{}, ui.ListState{}, -1, 0
	a.started = time.Time{}
	a.status = "No active connection"
	done := make(chan error, 1)
	if old == nil {
		close(done)
	} else {
		go func() { done <- old.Close(); close(done) }()
	}
	a.closingConnection = done
	return done
}

func (a *App) applyConnectionRemoval(remaining []savedConnection, expected *database.Client, active bool) <-chan error {
	a.setSavedConnections(remaining)
	if active && a.client == expected {
		done := a.disconnectConnection()
		a.lastConnection = nil
		return done
	}
	done := make(chan error)
	close(done)
	return done
}

func (a *App) deleteConnection(name string) {
	if a.connectionStoreBusy || a.busy {
		return
	}
	target := a
	for _, session := range a.connectionSessions() {
		if session.hasConnection() && session.connectionLabel() == name {
			target = session
			break
		}
	}
	active := target.hasConnection() && target.connectionLabel() == name
	expected := target.client
	changes := []credentialChange{}
	for _, saved := range a.availableConnections() {
		if saved.Name == name {
			changes = append(changes, credentialChange{key: credentialKey(name, saved.Config), credential: connectionCredential{Mode: passwordNever}})
		}
	}
	if active && len(changes) == 0 {
		changes = append(changes, credentialChange{key: credentialKey(name, target.client.Settings()), credential: connectionCredential{Mode: passwordNever}})
	}
	remaining := make([]savedConnection, 0, len(a.connections))
	found := false
	for _, saved := range a.connections {
		if saved.Name == name {
			found = true
		} else {
			remaining = append(remaining, saved)
		}
	}
	apply := func() {
		a.applyCredentials(changes)
		target.applyConnectionRemoval(remaining, expected, active)
		for _, session := range a.connectionSessions() {
			if session.lastConnection != nil && session.lastConnection.Name == name {
				session.lastConnection = nil
			}
		}
		a.status = "Connection deleted · " + name
	}
	if found {
		a.saveConnectionsWithCredentials(remaining, changes, apply)
	} else if active || len(changes) > 0 {
		apply()
	}
}

func (a *App) connectionMenu(m *ui.Menu) {
	if a.explorerEngine() == database.BigQuery {
		if m.Item("Add project…").Disabled(a.busy || a.connectionStoreBusy || a.client == nil).Chosen() {
			a.openBigQueryProject()
		}
	}
	if m.Item("Add connection…").Disabled(a.busy || a.connectionStoreBusy).Chosen() {
		a.open()
	}
	if m.Item("Modify connection…").Disabled(a.busy || a.connectionStoreBusy).Chosen() {
		a.editConnection(a.connectionLabel(), a.connectionSettings())
	}
	if m.Item("Delete connection").Disabled(a.busy || a.connectionStoreBusy || !a.hasConnection()).Chosen() {
		a.deleteConnection(a.connectionLabel())
	}
	m.Separator()
	if m.Item("Disconnect").Disabled(!a.hasConnection()).Chosen() {
		a.disconnectConnection()
	}
	if m.Item("Reconnect").Disabled(a.connectionStoreBusy || a.client == nil).Chosen() {
		a.requestConnection(savedConnection{Name: a.connectionLabel(), Config: a.connectionSettings()}, true)
	}
	if m.Item("Refresh").Disabled(a.busy || a.client == nil).Chosen() {
		a.refreshCatalog()
	}
}

func (a *App) savedConnectionMenu(m *ui.Menu, saved savedConnection) {
	if m.Item("Connect").Disabled(a.connectionStoreBusy).Chosen() {
		a.requestConnection(saved, false)
	}
	if m.Item("Reconnect").Disabled(a.connectionStoreBusy).Chosen() {
		a.requestConnection(saved, true)
	}
	if m.Item("Modify connection…").Disabled(a.busy || a.connectionStoreBusy).Chosen() {
		a.editConnection(saved.Name, saved.Config)
	}
	if m.Item("Delete connection").Disabled(a.busy || a.connectionStoreBusy).Chosen() {
		a.deleteConnection(saved.Name)
	}
}
