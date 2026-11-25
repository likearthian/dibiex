package app

import "github.com/egoist/mygo/ui"

func (a *App) openInspector() { a.activateConnection(); a.filesDrawer = false; a.showInspector = true }
func (a *App) toggleInspectorPane() {
	if a.showInspector && !a.filesDrawer {
		a.showInspector = false
	} else {
		a.openInspector()
	}
}
func (a *App) selectRightPane(c *ui.Context, inspector bool) {
	if inspector {
		a.openInspector()
		return
	}
	width, _ := c.Size()
	a.showFiles = true
	a.filesDrawer = width < 1100
	a.showInspector = a.filesDrawer
}
func (a *App) toggleFilesPane(c *ui.Context) {
	width, _ := c.Size()
	if width < 1100 {
		if a.showInspector && a.filesDrawer {
			a.showInspector = false
			a.filesDrawer = false
		} else {
			a.selectRightPane(c, false)
		}
		return
	}
	if a.showFiles && !a.showInspector {
		a.showFiles = false
	} else {
		a.selectRightPane(c, false)
	}
}
