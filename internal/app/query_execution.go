package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type queryExecution struct {
	results                     []*queryResultTab
	activeResult, pendingResult *queryResultTab
	nextResult                  int
	elapsed                     time.Duration
	running                     bool
	cancel                      context.CancelFunc
	started                     time.Time
	sql                         string
	line                        int
	result                      database.Result
	status, errorText           string
	grid                        ui.ListState
	selected, page              int
	output                      bool
}

func (a *App) storeQueryView(tab *queryTab) {
	if tab == nil {
		return
	}
	e := &tab.execution
	e.result = a.result
	e.grid, e.selected, e.page, e.output = a.grid, a.selected, a.page, a.output
	if snapshot := e.activeResult; snapshot != nil {
		snapshot.result, snapshot.grid, snapshot.selected, snapshot.page = e.result, e.grid, e.selected, e.page
	}
}

func (a *App) loadQueryView(tab *queryTab) {
	e := queryExecution{status: "No query selected", selected: -1, line: 1}
	if tab != nil {
		e = tab.execution
	}
	a.result, a.status, a.errorText = e.result, e.status, e.errorText
	a.grid, a.selected, a.page, a.output = e.grid, e.selected, e.page, e.output
	a.executionSQL, a.executionLine = e.sql, e.line
}

func (a *App) queryBusy() bool {
	t := a.currentEditor()
	return a.busy || t != nil && t.execution.running
}
func (a *App) queryStarted() time.Time {
	if t := a.currentEditor(); t != nil && t.execution.running {
		return t.execution.started
	}
	return a.started
}
func (a *App) stopQuery() {
	if t := a.currentEditor(); t != nil && t.execution.running {
		if t.execution.cancel != nil {
			t.execution.cancel()
		}
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) releaseTabSession(tab *queryTab) {
	tab.cancelCompletion()
	if tab.state.Buffer != nil {
		tab.state.ShowCompletions(-1, nil)
	}
	if tab.execution.cancel != nil {
		tab.execution.cancel()
	}
	if tab.session != nil {
		session := tab.session
		tab.session = nil
		go session.Close()
	}
}

func (a *App) cancelTabQueries() {
	for _, tab := range a.tabs {
		a.releaseTabSession(tab)
		tab.execution = queryExecution{status: "Disconnected", selected: -1, line: 1}
		tab.querySchemas = nil
	}
}

func (a *App) startQuery(query string, write bool) {
	tab := a.currentEditor()
	if tab == nil || a.queryBusy() || a.client == nil || a.win == nil || strings.TrimSpace(query) == "" {
		return
	}
	a.storeQueryView(tab)
	if !a.prepareResultTab(tab) {
		return
	}
	timeout := 30 * time.Second
	if a.client.Engine() == database.BigQuery {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(a.connectionContext(), timeout)
	e := &tab.execution
	e.running, e.cancel, e.started, e.sql, e.line = true, cancel, time.Now(), query, max(1, a.executionLine)
	e.status, e.errorText, e.output = "Running…", "", write
	if tab.tableView == nil {
		a.hideResults = false
	}
	a.loadQueryView(tab)
	client, win, pipeline, session := a.client, a.win, a.introspector, tab.session
	projects := a.connectionSettings().BigQueryProjects()
	useCursor := tab.cursorFetch && !write && client.Engine() == database.PostgreSQL && postgresCursorQuery(query)
	started, line := e.started, e.line
	browse := tab.tableView != nil
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				win.Update(func() {})
			}
		}
	}()
	go func() {
		defer close(done)
		defer cancel()
		var result database.Result
		var cursor *database.PostgreSQLCursor
		var err error
		if session == nil {
			session, err = client.NewQuerySession(ctx)
		}
		if err == nil {
			if write {
				result, err = session.ExecuteResult(ctx, query)
			} else if useCursor {
				cursor, result, err = session.StartPostgreSQLCursor(ctx, query)
			} else {
				result, err = session.Query(ctx, query)
			}
		}
		if browse || errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) {
			if session != nil {
				session.Close()
			}
			session = nil
		}
		var schemas []database.Schema
		var schemaErr error
		if err == nil && !browse {
			if write && pipeline != nil {
				pipeline.Invalidate()
			}
			if client.Engine() == database.SQLite {
				schemas, schemaErr = session.Catalog(ctx)
			} else if !useCursor && (client.Engine() != database.BigQuery || write) {
				if client.Engine() == database.BigQuery && pipeline != nil {
					schemas, schemaErr = pipeline.BigQueryCatalog(ctx, projects, write)
				} else if pipeline != nil {
					schemas, schemaErr = pipeline.Catalog(ctx, write)
				} else {
					schemas, schemaErr = client.Catalog(ctx)
				}
			}
		}
		elapsed := time.Since(started)
		win.Update(func() {
			open := false
			for _, candidate := range a.tabs {
				if candidate == tab {
					open = true
					break
				}
			}
			if a.closed || a.client != client || !open {
				if session != nil {
					go session.Close()
				}
				return
			}
			tab.session = session
			if session == nil {
				tab.querySchemas = nil
			}
			target := tab.execution.pendingResult
			a.finishTabQuery(tab, result, err, query, write, line, elapsed, schemaErr)
			if err == nil && target != nil {
				target.cursor = cursor
			}
			if err == nil {
				if schemaErr == nil && schemas != nil {
					if client.Engine() == database.BigQuery {
						schemas = mergeBigQueryCatalog(a.schemas, schemas, projects)
						schemas = visibleBigQueryCatalog(schemas, a.bigQueryProjects())
					}
					tab.querySchemas = schemas
					if client.Engine() != database.SQLite || a.currentEditor() == tab {
						a.applyCatalog(schemas, false)
					}
				}
				if write {
					a.invalidateMetadata()
					a.loadExplorerCatalog(true)
				}
			}
		})
	}()
}

func (a *App) finishTabQuery(tab *queryTab, result database.Result, err error, query string, write bool, line int, elapsed time.Duration, schemaErr error) {
	e := &tab.execution
	if a.currentEditor() == tab {
		a.storeQueryView(tab)
	}
	if e.pendingResult == nil {
		if !a.prepareResultTab(tab) {
			return
		}
	}
	target := e.pendingResult
	e.running, e.cancel = false, nil
	e.elapsed = elapsed
	if err != nil {
		e.errorText, e.status, e.output = err.Error(), "Query failed", true
		if row, col := queryErrorPosition(err, query); row > 0 {
			e.errorText += fmt.Sprintf("\nEditor line %d, column %d", line+row-1, col)
		}
	} else {
		target.view = resultView{}
		target.result, target.selected, target.page, target.grid, target.sql = result, -1, 0, ui.ListState{}, query
		if e.activeResult == target {
			e.result, e.selected, e.page, e.grid = result, -1, 0, ui.ListState{}
		}
		e.status = fmt.Sprintf("%d rows · %s", len(result.Rows), result.Elapsed.Round(time.Microsecond))
		if write {
			e.status = fmt.Sprintf("%d rows affected · %s", result.RowsAffected, result.Elapsed.Round(time.Microsecond))
		}
		if result.Truncated {
			e.status += " · result limit reached"
		}
		if result.More {
			e.status += " · more available (cursor idle timeout: 1 minute)"
		}
		if schemaErr != nil {
			e.status += " · schema refresh failed: " + schemaErr.Error()
		}
	}
	e.pendingResult = nil
	if a.currentEditor() == tab {
		a.loadQueryView(tab)
	}
}

func (a *App) catalogRunner() (*database.Client, *database.Introspector) {
	if a.client != nil && a.client.Engine() == database.SQLite {
		if tab := a.currentEditor(); tab != nil && tab.session != nil {
			return tab.session, nil
		}
	}
	return a.client, a.introspector
}

func (a *App) selectQueryCatalog(tab *queryTab) {
	if a.client == nil || a.client.Engine() != database.SQLite {
		return
	}
	a.invalidateMetadata()
	if tab != nil && tab.querySchemas != nil {
		a.applyCatalog(tab.querySchemas, false)
		return
	}
	var schemas []database.Schema
	for _, schema := range a.schemas {
		if schema.Name == "main" {
			schemas = append(schemas, schema)
		}
	}
	a.applyCatalog(schemas, false)
}
