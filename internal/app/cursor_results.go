package app

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func postgresCursorQuery(query string) bool {
	_, end := statementRange(query, 0, database.PostgreSQL)
	if strings.TrimSpace(query[end:]) != "" {
		return false
	}
	query = strings.TrimSpace(query)
	for {
		if strings.HasPrefix(query, "--") {
			end := strings.IndexByte(query, '\n')
			if end < 0 {
				return false
			}
			query = strings.TrimSpace(query[end+1:])
			continue
		}
		if strings.HasPrefix(query, "/*") {
			depth, i := 1, 2
			for i < len(query) && depth > 0 {
				if strings.HasPrefix(query[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(query[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return false
			}
			query = strings.TrimSpace(query[i:])
			continue
		}
		break
	}
	i := 0
	for i < len(query) && ((query[i] >= 'a' && query[i] <= 'z') || (query[i] >= 'A' && query[i] <= 'Z') || query[i] == '_') {
		i++
	}
	switch strings.ToUpper(query[:i]) {
	case "SELECT", "WITH", "VALUES":
		return true
	}
	return false
}

func cursorOpen(snapshot *queryResultTab) bool {
	if snapshot == nil || snapshot.cursor == nil {
		return false
	}
	select {
	case <-snapshot.cursor.Done():
		return false
	default:
		return true
	}
}

func (a *App) cursorResultControls(c *ui.Context, k palette, tab *queryTab, snapshot *queryResultTab) {
	if snapshot == nil || snapshot.cursor == nil {
		return
	}
	open := cursorOpen(snapshot)
	if open {
		if ui.Button(c, "Fetch next 1,000").Disabled(a.queryBusy() || snapshot.pinned || !snapshot.result.More).Clicked() {
			a.fetchCursorResult(tab, snapshot)
		}
		if ui.Button(c, "Close cursor").Disabled(a.queryBusy()).Clicked() {
			cursor := snapshot.cursor
			snapshot.cursor = nil
			snapshot.result.Truncated = snapshot.result.Truncated || snapshot.result.More
			snapshot.result.More = false
			if tab.execution.activeResult == snapshot {
				tab.execution.result = snapshot.result
				a.result = snapshot.result
			}
			go cursor.Close()
		}
	} else if snapshot.result.More {
		ui.Text(c, "Cursor closed · rerun to fetch more").FontSize(11).TextColor(k.muted)
	}
}

func (a *App) fetchCursorResult(tab *queryTab, target *queryResultTab) {
	if tab == nil || a.queryBusy() || a.win == nil || target.pinned || !cursorOpen(target) {
		return
	}
	a.storeQueryView(tab)
	previous := target.result
	bytes := 0
	for _, row := range previous.Rows {
		for _, cell := range row {
			bytes += len(cell)
		}
	}
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	e := &tab.execution
	e.running, e.cancel, e.started, e.pendingResult = true, cancel, time.Now(), target
	e.status, e.errorText = "Fetching next 1,000 rows…", ""
	client, win, cursor, session := a.client, a.win, target.cursor, tab.session
	a.loadQueryView(tab)
	go func() {
		defer cancel()
		batch, err := cursor.Fetch(ctx, database.PostgreSQLCursorMaxRows-len(previous.Rows), database.PostgreSQLCursorMaxBytes-bytes)
		deadSession := errors.Is(err, driver.ErrBadConn)
		if deadSession && session != nil {
			_ = session.Close()
		}
		if err == nil && len(previous.Rows)+len(batch.Rows) >= database.PostgreSQLCursorMaxRows && batch.More {
			batch.More = false
			batch.Truncated = true
			_ = cursor.Close()
		}
		win.Update(func() {
			if a.closed || a.client != client {
				return
			}
			found := false
			for _, candidate := range a.tabs {
				if candidate == tab {
					found = true
					break
				}
			}
			if !found {
				return
			}
			if deadSession && tab.session == session {
				tab.session = nil
				tab.querySchemas = nil
			}
			a.finishCursorFetch(tab, target, previous, batch, err)
		})
	}()
}

func (a *App) finishCursorFetch(tab *queryTab, target *queryResultTab, previous, batch database.Result, err error) {
	if a.currentEditor() == tab {
		a.storeQueryView(tab)
	}
	e := &tab.execution
	e.running, e.cancel, e.pendingResult = false, nil, nil
	if err != nil {
		e.errorText, e.status, e.output = err.Error(), "Fetch failed", true
	} else {
		// A new backing slice preserves any retained snapshots of earlier rows.
		combined := append(append([][]string(nil), previous.Rows...), batch.Rows...)
		target.result.Rows = combined
		target.result.More, target.result.Truncated = batch.More, batch.Truncated
		target.result.Elapsed += batch.Elapsed
		e.status = fmt.Sprintf("%d rows loaded", len(combined))
		if batch.More {
			e.status += " · more available"
		}
		if batch.Truncated {
			e.status += " · result limit reached"
		}
		e.elapsed = target.result.Elapsed
		if e.activeResult == target {
			e.result = target.result
		}
	}
	if a.currentEditor() == tab {
		a.loadQueryView(tab)
	}
}
