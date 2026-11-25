package app

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"time"
)

const sidePanelGap float32 = 8

func (a *App) view(c *ui.Context) {
	if a.connectionWorkspace != nil {
		a = a.connectionWorkspace.active
	}
	a.workspaceView(c)
	if a.connectionWorkspace != nil {
		a = a.connectionWorkspace.active
	}
	a.syncConnectionWindow()
}

func (a *App) workspaceView(c *ui.Context) {
	width, _ := c.Size()
	if !a.showInspector {
		a.filesDrawer = false
	}
	if width >= 1100 && a.filesDrawer {
		a.filesDrawer = false
		a.showInspector = false
		a.showFiles = true
	}
	if a.themeMode != "" {
		theme := ui.DarkTheme()
		if a.themeMode == "light" {
			theme = ui.LightTheme()
		}
		c.SetTheme(theme)
	}
	k := paletteOf(c)
	theme := *c.Theme()
	theme.Background, theme.Surface, theme.Border = k.bg, k.editor, k.border
	theme.Text, theme.TextMuted, theme.Accent, theme.Focus = k.text, k.muted, k.accent, k.accent
	theme.SurfaceHover, theme.SurfacePressed, theme.Selection = k.border, k.selected, k.selected
	theme.Font = "Inter"
	theme.Radius, theme.FontSize, theme.Spacing = 4, 13, 3
	c.SetTheme(&theme)
	for _, owner := range a.connectionSessions() {
		for _, toast := range owner.pendingToasts {
			c.AddToast(toast)
		}
		owner.pendingToasts = nil
	}
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyEnter) && !a.queryBusy() && !a.showBigQueryProject && !a.showConnection && !a.showCreateTable && !a.showQueryName && !a.showTableAction && !a.showTableData && !a.showTableERD && !a.showResultExport && !a.showQueryParams {
		a.openQueryExport()
	} else if c.Shortcut(ui.Cmd, ui.KeyEnter) && !a.queryBusy() && !a.showBigQueryProject && !a.showConnection && !a.showCreateTable && !a.showQueryName && !a.showTableAction && !a.showTableData && !a.showTableERD && !a.showResultExport && !a.showQueryParams {
		a.run(false)
	}
	if c.Shortcut(ui.Cmd, ui.KeyO) && !a.showBigQueryProject && !a.showConnection && !a.showCreateTable && !a.showQueryName && !a.showTableAction && !a.showTableData && !a.showTableERD && !a.showResultExport && !a.showQueryParams {
		a.open()
	}
	if c.Shortcut(ui.Cmd, ui.KeyS) && !a.showBigQueryProject && !a.showConnection && !a.showCreateTable && !a.showQueryName && !a.showTableAction && !a.showTableData && !a.showTableERD && !a.showResultExport && !a.showQueryParams {
		a.saveQueryDialog(a.currentEditor())
	}
	if c.Shortcut(ui.Alt, ui.Key1) {
		a.hideExplorer = !a.hideExplorer
	}
	if c.Shortcut(ui.Alt, ui.Key8) {
		a.hideResults = !a.hideResults
	}
	chrome := k.bg
	if c.Vibrancy() {
		chrome = ui.Transparent
	}
	c.Root().Background(chrome)
	ui.Column(c).Fill().Background(chrome).Children(func() {
		a.titleBar(c, k)
		ui.Row(c).Grow(1).MinHeight(0).Padding(0, 8).AlignItems(ui.Stretch).Children(func() {
			if a.hideExplorer {
				ui.Column(c).Width(36).Padding(4).BorderWidth(0, 1, 0, 0).BorderColor(k.border).Children(func() {
					if toolButton(c, k, "database", "Show Database Explorer · Alt+1", false).Clicked() {
						a.hideExplorer = false
					}
				})
				a.workArea(c, k)
			} else {
				reserved := float32(480)
				if width >= 1100 && (a.showInspector || a.showFiles) {
					reserved += 278
				}
				maxWidth := max(float32(170), width-reserved)
				a.explorerWidth = max(float32(170), min(a.explorerWidth, maxWidth))
				paneSplit(c.Key("explorer-split"), k, "Resize Database Explorer", &a.explorerWidth, sidePanelGap, false, func() { a.sidebar(c, k) }, func() { a.workArea(c, k) }).Grow(1)
			}
		})
		ui.Row(c).Height(28).Padding(0, 14).Gap(16).AlignItems(ui.Center).Children(func() {
			a.objectBreadcrumb(c, k)
			ui.Text(c, a.status).SingleLine().MaxWidth(180).MinWidth(0).FontSize(11).TextColor(k.muted).Tooltip(a.status)
			ui.Text(c, a.engineLabel()).FontSize(11).TextColor(k.muted)
			ui.Text(c, "UTF-8").FontSize(11).TextColor(k.muted)
			ui.Text(c, "4 spaces").FontSize(11).TextColor(k.muted)
		})
	})
	a.queryNameModal(c, k)
	a.connectionDialog(c, k)
	a.bigQueryProjectDialog(c, k)
	if width < 1100 {
		_, height := c.Size()
		ui.DialogBase(c.Key("inspector-drawer"), &a.showInspector, func(back, panel ui.Element) {
			back.Background(ui.RGBA(0, 0, 0, 0.25))
			panel.Absolute().Right(8).Top(44).Width(270).Height(height - 72).Label("Right sidebar")
			if a.filesDrawer {
				a.filesPane(c, k)
			} else {
				a.inspector(c, k)
			}
		})
	}
	a.createTableDialog(c, k)
	a.tableActionDialog(c, k)
	a.tableDataDialog(c, k)
	a.tableERDDialog(c, k)
	a.resultExportDialog(c, k)
	a.queryParamsModal(c, k)
}
func (a *App) workArea(c *ui.Context, k palette) {
	width, _ := c.Size()
	row := ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	total := width - 16
	if a.hideExplorer {
		row.Padding(0, 0, 0, 7)
		total -= 36 + 7
	} else {
		total -= a.explorerWidth + sidePanelGap
	}
	row.Children(func() {
		if width < 1100 || !a.showInspector && !a.showFiles {
			a.workspace(c, k)
			return
		}
		// The splitter edits the left pane after input. Convert that
		// back to a right width before adapting to window/explorer resizing.
		if a.rightSplitTotal > 0 {
			a.rightWidth = a.rightSplitTotal - a.rightSplitWidth - sidePanelGap
		}
		if a.rightWidth == 0 {
			a.rightWidth = 270
		}
		a.rightWidth = max(float32(220), min(a.rightWidth, min(float32(600), total-369)))
		a.rightSplitWidth = total - a.rightWidth - sidePanelGap
		a.rightSplitTotal = total
		paneSplit(c.Key("right-sidebar-split"), k, "Resize right sidebar", &a.rightSplitWidth, sidePanelGap, false, func() {
			a.workspace(c, k)
		}, func() {
			if a.showInspector {
				a.inspector(c, k)
			} else {
				a.filesPane(c, k)
			}
		}).Grow(1).MinWidth(0).MinHeight(0)
	})
}
func (a *App) workspace(c *ui.Context, k palette) {
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Radius(10).Border(1, paneOutline(c)).Clip().Background(k.editor).Children(func() {
		if a.hideResults || a.currentEditor() != nil && a.currentEditor().tableView != nil {
			a.queryPane(c, k)
		} else {
			paneSplit(c.Key("query-results-split"), k, "Resize editor and results", &a.editorPercent, paneGripSize, true, func() { a.queryPane(c, k) }, func() { a.resultsPane(c, k) }).Grow(1)
		}
	})
}
func (a *App) queryPane(c *ui.Context, k palette) {
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Background(k.editor).Children(func() {
		ui.Row(c).Height(40).Padding(0, 6).Gap(4).Background(k.panel).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
			ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Height(40).Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					for _, ref := range a.workspaceTabs() {
						if ref.owner.editorTab(c, k, ref.tab, ref.owner == a && ref.index == a.activeTab).Clicked() {
							ref.owner.selectQueryTab(ref.index)
						}
					}
				})
			})
			if toolbarAction(c, k, "plus", "New query tab", k.muted).Clicked() {
				a.addQuery("")
			}
			if toolbarAction(c, k, "folder-open", "Open SQL file", k.muted).Clicked() {
				a.openSQLFile()
			}
			toolbarAction(c, k, "ellipsis", "Console options", k.muted).PopoverMenuButton(func(m *ui.Menu) {
				if m.Item("New query tab").Chosen() {
					a.addQuery("")
				}
				if m.Item("Inspector").Chosen() {
					a.toggleInspectorPane()
				}
				if m.Item("Services · Show results").Chosen() {
					a.hideResults = false
				}
			})
		})
		tab := a.currentEditor()
		if tab != nil && tab.tableView != nil {
			a.tableViewPane(c, k, tab)
			return
		}
		if tab == nil {
			ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Children(func() {
				ui.Icon(c, controlIcon("console")).Size(28, 28).TextColor(k.muted)
				ui.Text(c, "No query tabs").FontSize(15).TextColor(k.text)
				ui.Text(c, "Create a query, open an SQL file, or choose a saved query in Files.").FontSize(12).TextColor(k.muted)
			})
			return
		}
		sectionBar(c, k).Children(func() {
			if toolbarAction(c, k, "save", "Save query", k.muted).Disabled(a.ensureWorkspace().queryStoreBusy).Clicked() {
				a.saveQueryDialog(tab)
			}
			toolbarDivider(c, k)
			if a.client != nil && a.client.Engine() == "PostgreSQL" {
				ui.Checkbox(c, &tab.cursorFetch, "Cursor fetch").Disabled(a.queryBusy()).Tooltip("Fetch SELECT results in batches of 1,000 using a read-only transaction. Finish your editor transaction first. The cursor closes after 1 idle minute or on rerun.")
			}
			if toolbarAction(c, k, "play", "Execute", k.green).Disabled(a.queryBusy() || a.client == nil).Tooltip("Execute · Ctrl/Cmd+Enter · Run selection or active statement").Clicked() {
				a.run(false)
			}
			if toolbarAction(c, k, "execute-file", "Execute to File", k.muted).Disabled(a.queryBusy() || a.client == nil).Tooltip("Execute to File · Ctrl/Cmd+Shift+Enter · Stream selection or active statement directly to a file").Clicked() {
				a.openQueryExport()
			}
			if toolbarAction(c, k, "explain", "Explain", k.muted).Disabled(a.queryBusy() || a.client == nil || a.client.Engine() == "BigQuery").Tooltip("Explain · Query plan without executing the statement; unavailable in BigQuery").Clicked() {
				a.explain()
			}
			if toolbarAction(c, k, "settings", "Parameters", k.muted).Disabled(a.queryBusy() || a.client == nil).Tooltip("Parameters · Set parameter values for the active query").Clicked() {
				a.openQueryParamsDialog()
			}
			toolbarDivider(c, k)
			if toolbarAction(c, k, "square", "Stop", k.muted).Disabled(!a.queryBusy()).Clicked() {
				a.stopQuery()
			}
			if toolbarAction(c, k, "execute-write", "Execute writes", k.muted).Disabled(a.queryBusy() || a.client == nil).Tooltip("Execute writes · Execute selection or active statement without fetching rows").Clicked() {
				a.run(true)
			}
			toolbarDivider(c, k)
			if toolButton(c, k, "table", "Toggle Services · Alt+8", !a.hideResults).Clicked() {
				a.hideResults = !a.hideResults
			}
			if a.queryBusy() && !a.queryStarted().IsZero() {
				ui.Text(c, time.Since(a.queryStarted()).Round(100*time.Millisecond).String()).FontSize(11).TextColor(k.accent)
			}
			ui.Spacer(c)
			ui.Text(c, a.workspaceName()).SingleLine().MaxWidth(180).FontSize(11).TextColor(k.muted).Tooltip("Query connection: " + a.workspaceName())
			ui.Icon(c, controlIcon("schema")).Size(15, 15).TextColor(k.muted)
			ui.Text(c, a.engineLabel()).FontSize(11).TextColor(k.muted)
			if a.inspectorTable.Schema != "" {
				ui.Text(c, "· "+a.inspectorTable.Schema).SingleLine().MaxWidth(140).FontSize(11).TextColor(k.muted)
			}
		})
		a.sqlEditor(c)
	})
}
func (a *App) resultsPane(c *ui.Context, k palette) {
	ui.Column(c).Fill().MinHeight(0).Background(k.editor).Children(func() {
		a.resultTabStrip(c, k)
		if a.output {
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(16).Gap(10).Children(func() {
					ui.Text(c, a.status).Font("JetBrains Mono").FontSize(12).TextColor(k.muted)
					if a.errorText != "" {
						ui.Text(c, fmt.Sprintf("Statement starts at editor line %d · runtime %s", max(1, a.executionLine), a.outputElapsed().Round(time.Microsecond))).FontSize(11).TextColor(k.muted)
						ui.Text(c, a.errorText).Font("JetBrains Mono").FontSize(12).TextColor(k.danger)
					}
				})
			})
			return
		}
		a.resultGrid(c, k)
	})
}
