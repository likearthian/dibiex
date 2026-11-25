package app

type workspaceTab struct {
	owner *App
	tab   *queryTab
	index int
}

func (a *App) hasBoundDrafts(name string) bool {
	if len(a.tabs) == 0 {
		return false
	}
	if a.lastConnection != nil && a.lastConnection.Name != name {
		return true
	}
	if a.queryWorkspace != "" && a.queryWorkspace != name {
		return true
	}
	if a.connectionWorkspace != nil {
		for _, tab := range a.tabs {
			for _, record := range a.connectionWorkspace.queries {
				if tab.savedID == record.ID && record.Connection != name {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) workspaceName() string {
	if a.hasConnection() {
		return a.connectionLabel()
	}
	if a.lastConnection != nil {
		return a.lastConnection.Name
	}
	if a.queryWorkspace != "" {
		return a.queryWorkspace
	}
	return "Unassigned"
}

func (a *App) workspaceTabs() []workspaceTab {
	var tabs []workspaceTab
	for _, owner := range a.connectionSessions() {
		for i, tab := range owner.tabs {
			tabs = append(tabs, workspaceTab{owner, tab, i})
		}
	}
	return tabs
}

func (a *App) selectQueryTab(index int) {
	if index < 0 || index >= len(a.tabs) {
		return
	}
	if previous := a.currentEditor(); previous != nil && previous != a.tabs[index] {
		previous.cancelCompletion()
		previous.state.ShowCompletions(-1, nil)
	}
	a.storeQueryView(a.currentEditor())
	a.activateConnection()
	a.activeTab = index
	a.selectQueryCatalog(a.currentEditor())
	a.loadQueryView(a.currentEditor())
	a.query = a.tabs[index].state.Buffer.String()
}

func (a *App) closeQueryTab() {
	if t := a.currentEditor(); t != nil {
		a.closeTab(t)
	}
}

func (a *App) closeTab(target *queryTab) {
	index := -1
	for i, t := range a.tabs {
		if t == target {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	selected := a.currentEditor()
	a.storeQueryView(selected)
	a.releaseTabSession(target)
	active := a.connectionWorkspace == nil || a.connectionWorkspace.active == a
	a.tabs = append(a.tabs[:index], a.tabs[index+1:]...)
	if len(a.tabs) > 0 {
		if selected != target {
			for i, t := range a.tabs {
				if t == selected {
					a.activeTab = i
					break
				}
			}
		} else {
			a.activeTab = min(index, len(a.tabs)-1)
		}
		a.query = a.tabs[a.activeTab].state.Buffer.String()
		a.selectQueryCatalog(a.tabs[a.activeTab])
		a.loadQueryView(a.tabs[a.activeTab])
		return
	}
	a.activeTab, a.query = 0, ""
	a.loadQueryView(nil)
	if active {
		remaining := a.workspaceTabs()
		if len(remaining) > 0 {
			next := remaining[len(remaining)-1]
			next.owner.selectQueryTab(next.index)
		}
	}
}

func (a *App) consoleLabel(name string) string {
	if len(a.connectionSessions()) > 1 {
		return name + " · " + a.workspaceName()
	}
	return name
}
