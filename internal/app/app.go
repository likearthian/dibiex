package app

import (
	"context"
	"log"
	"time"

	"dbcraft/assets"
	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var monoFont = assets.MonoFont

func init() {
	for _, font := range [][]byte{assets.InterfaceFont, assets.InterfaceSemiboldFont} {
		if err := ui.RegisterFont(font, "Inter"); err != nil {
			log.Fatalf("Register Inter font: %v", err)
		}
	}
}

type App struct {
	version                                      string
	catalogPicker                                catalogPicker
	resultExport                                 *resultExportForm
	showResultExport                             bool
	showQueryParams                              bool
	queryParams                                  *queryParamsDialog
	showBigQueryProject                          bool
	bigQueryProjectID, bigQueryProjectError      string
	pendingToasts                                []ui.Toast
	tableMenuState                               tableMenuState
	tableAction                                  *tableActionForm
	showTableAction                              bool
	tableData                                    *tableDataForm
	showTableData                                bool
	tableERD                                     *tableERD
	showTableERD                                 bool
	queryWorkspace                               string
	queryName                                    queryNameDialog
	showQueryName, mainMenuOpen                  bool
	showCreateTable                              bool
	tableDesigner                                *tableDesigner
	sqliteDesigner                               *sqliteTableDesigner
	lastConnection                               *savedConnection
	filesGroupClosed                             bool
	closingConnection                            <-chan error
	connectionWorkspace                          *connectionWorkspace
	explorerCatalog                              database.ExplorerCatalog
	explorerLoading                              bool
	explorerError                                string
	win                                          *mygo.Window
	connectionWindow                             *mygo.Window
	client                                       *database.Client
	query, connection, status, errorText         string
	schemas                                      []database.Schema
	result                                       database.Result
	grid                                         ui.ListState
	selected                                     int
	busy, closed                                 bool
	connecting                                   bool
	cancel                                       context.CancelFunc
	explorerWidth, editorPercent                 float32
	rightWidth, rightSplitWidth, rightSplitTotal float32
	filter, activeTable                          string
	explorerSearch                               explorerSchemaSearch
	hideExplorer, hideResults, output            bool
	treeClosed                                   map[string]bool
	schemaErrors                                 map[string]string
	schemaLoading                                map[string]*uint64
	showConnection                               bool
	connectionForm                               connectionForm
	tabs                                         []*queryTab
	activeTab, nextTab, page                     int
	showInspector, inspectorDDL                  bool
	metadata                                     map[string]database.TableMetadata
	metadataErrors                               map[string]string
	metadataLoading                              map[string]bool
	metadataVersion                              uint64
	inspectorTable                               database.Table
	inspectorObject                              *catalogInspection
	inspectorColumn                              *columnInspection
	inspectorDefinition                          *tableObjectInspection
	objectPath                                   []string
	breadcrumbScroll                             ui.ScrollState
	breadcrumbWindowWidth                        float32
	started                                      time.Time
	executionSQL                                 string
	executionLine                                int
	connections                                  []savedConnection
	connectionStoreBusy                          bool
	introspector                                 *database.Introspector
	cacheWarning                                 string
	fieldOpen                                    map[string]bool
	tableSectionsOpen, tableSectionLoading       map[string]bool
	tableSectionErrors                           map[string]string
	propertyOpen                                 map[string]bool
	inspectorProperties                          bool
	showFiles, filesCollapsed                    bool
	filesDrawer                                  bool
	hideSearch                                   bool
	themeMode, selectedTree                      string
	sessionContext                               context.Context
	sessionCancel                                context.CancelFunc
}

func Run(version string) {
	a := &App{version: version, status: "Add a connection to begin", selected: -1, explorerWidth: 260, editorPercent: 60, showFiles: true, hideResults: true, hideSearch: true, themeMode: "dark"}
	ui.RegisterFont(monoFont, "JetBrains Mono")
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}, {Role: mygo.RoleEditMenu}}))
	mygo.App.WhenReady(func() {
		mygo.Theme.SetSource(mygo.ThemeDark)
		opts := mygo.WindowOptions{Title: "dibiex", Width: 1180, Height: 780,
			MinWidth: 800, MinHeight: 560, StateKey: "main", BackgroundColor: "#252629",
			Vibrancy:      mygo.VibrancyMica,
			TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: &mygo.Point{X: 16, Y: 15}, Content: ui.View(a.view)}
		if ownControls {
			opts.TitleBarStyle, opts.Frameless = mygo.TitleBarDefault, true
		}
		a.win = mygo.NewWindow(opts)
		a.loadConnections()
		a.loadQueries()
		a.ensureWorkspace().loadResultSettings(a.win)
		if err := a.win.SetIcon(assets.AppIcon); err != nil {
			log.Printf("Set window icon: %v", err)
		}
		a.win.OnClosed(func() {
			a.cancelConnections()
			mygo.App.Quit()
		})
	})
	mygo.App.OnQuit(func() {
		a.cancelConnections()
		for _, session := range a.connectionSessions() {
			if session.client != nil {
				session.client.Close()
			}
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *App) run(write bool) {
	if tab := a.currentEditor(); tab != nil && tab.tableView != nil {
		if !write {
			a.startQuery(tab.state.Buffer.String(), false)
		}
		return
	}
	a.runAction(a.querySQL(), write, "run")
}

func (a *App) runSQL(query string, write bool) { a.startQuery(query, write) }
