package app

import (
	"context"
	"fmt"
	"time"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) countTableRows(table database.Table) {
	if a.client == nil || a.win == nil {
		return
	}
	client, win, connection := a.client, a.win, a.connectionLabel()
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.status = "Counting rows · " + table.Label()
	go func() {
		defer cancel()
		session, err := client.NewQuerySession(ctx)
		var count int64
		if err == nil {
			defer session.Close()
			count, err = session.CountRows(ctx, table)
		}
		win.Update(func() { a.completeRowCount(client, table, connection, count, err) })
	}()
}

func (a *App) completeRowCount(client *database.Client, table database.Table, connection string, count int64, err error) {
	if a.closed || a.client != client {
		return
	}
	toast := ui.Toast{Title: fmt.Sprintf("%s · %d rows", table.Label(), count), Description: connection, Type: "success", Timeout: 8 * time.Second}
	if err != nil {
		toast.Title = "Could not count rows · " + table.Label()
		toast.Description = connection + " · " + err.Error()
		toast.Type = "error"
	}
	a.status = toast.Title
	a.pendingToasts = append(a.pendingToasts, toast)
}
