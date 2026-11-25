package app

import (
	"context"
	"fmt"

	"dibiex/internal/database"
)

// Each connection owns its clients, workers, editor tabs, results and catalog UI.
// Sharing only workspace coordination keeps worker callbacks tied to their owner
// even when another connection is selected.
type connectionWorkspace struct {
	resultSettings resultSettings
	fileMenuID     string
	fileMenuOpen   bool
	sessions       []*App
	active         *App
	credentials    map[string]connectionCredential
	queries        []savedQuery
	queryStoreBusy bool
	queriesLoaded  bool
}

func (a *App) connectionSessions() []*App {
	if a.connectionWorkspace == nil {
		return []*App{a}
	}
	return a.connectionWorkspace.sessions
}

func (a *App) ensureWorkspace() *connectionWorkspace {
	if a.connectionWorkspace == nil {
		a.connectionWorkspace = &connectionWorkspace{sessions: []*App{a}, active: a}
	}
	return a.connectionWorkspace
}

func (a *App) activateConnection() {
	w := a.ensureWorkspace()
	previous := w.active
	if previous != a {
		if tab := previous.currentEditor(); tab != nil {
			tab.cancelCompletion()
			tab.state.ShowCompletions(-1, nil)
		}
		a.explorerWidth, a.editorPercent = previous.explorerWidth, previous.editorPercent
		a.rightWidth, a.rightSplitWidth, a.rightSplitTotal = previous.rightWidth, previous.rightSplitWidth, previous.rightSplitTotal
		a.hideExplorer, a.hideSearch, a.themeMode = previous.hideExplorer, previous.hideSearch, previous.themeMode
		a.showFiles, a.showInspector, a.filesDrawer = previous.showFiles, previous.showInspector, previous.filesDrawer
		a.filesCollapsed, a.hideResults = previous.filesCollapsed, previous.hideResults
	}
	w.active = a
}

func (a *App) newConnectionSession() *App {
	w := a.ensureWorkspace()
	next := &App{connectionWorkspace: w, win: a.win, selected: -1, hideResults: true,
		showFiles: a.showFiles, connections: a.connections, connectionStoreBusy: a.connectionStoreBusy}
	w.sessions = append(w.sessions, next)
	next.activateConnection()
	return next
}

func (a *App) cancelConnections() {
	for _, session := range a.connectionSessions() {
		session.closed = true
		for _, tab := range session.tabs {
			tab.cancelCompletion()
			if tab.execution.cancel != nil {
				tab.execution.cancel()
			}
		}
		if session.sessionCancel != nil {
			session.sessionCancel()
		}
		if session.cancel != nil {
			session.cancel()
		}
	}
	if a.connectionWorkspace != nil {
		a.connectionWorkspace.credentials = nil
	}
}

func (a *App) setConnectionStoreBusy(busy bool) {
	for _, session := range a.connectionSessions() {
		session.connectionStoreBusy = busy
	}
}

func (a *App) setSavedConnections(connections []savedConnection) {
	for _, session := range a.connectionSessions() {
		session.connections = connections
	}
	if a.connectionWorkspace != nil && a.connectionWorkspace.queriesLoaded {
		a.ensureQueryOwners()
	}
}

func (a *App) installConnection(client *database.Client, pipeline *database.Introspector, schemas []database.Schema, name, warning string) *App {
	if a.hasConnection() || a.hasBoundDrafts(name) {
		a = a.newConnectionSession()
	}
	a.activateConnection()
	a.sessionContext, a.sessionCancel = context.WithCancel(context.Background())
	a.client, a.connection = client, name
	a.queryWorkspace = name
	a.introspector, a.cacheWarning = pipeline, warning
	a.applyCatalog(schemas, true)
	a.treeClosed["connection"] = false
	if client.Engine() == database.BigQuery {
		a.discoverCatalogProjects()
	}
	a.status = fmt.Sprintf("Connected · %s · %s", client.Engine(), name)
	return a
}
