package app

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const maxSQLFileBytes = 4 << 20

type savedQuery struct{ ID, Name, SQL, Connection string }
type queryNameDialog struct {
	tab                      *queryTab
	savedID, name, errorText string
}

func queryLibraryPath() (string, error) {
	path, err := connectionPath()
	return filepath.Join(filepath.Dir(path), "queries.json"), err
}

func readQueryLibrary(path string) ([]savedQuery, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("saved query library exceeds 32 MiB")
	}
	var records []savedQuery
	err = json.Unmarshal(data, &records)
	if err == nil {
		err = validateQueryLibrary(records)
	}
	return records, err
}

func validateQueryLibrary(records []savedQuery) error {
	ids := map[string]bool{}
	for _, record := range records {
		if record.ID == "" || ids[record.ID] || validQueryName(record.Name) != nil || strings.TrimSpace(record.Connection) == "" || !utf8.ValidString(record.SQL) || strings.ContainsRune(record.SQL, 0) {
			return fmt.Errorf("saved query library contains an invalid or duplicate entry")
		}
		ids[record.ID] = true
	}
	return nil
}

func writeQueryLibrary(path string, records []savedQuery) error {
	if err := validateQueryLibrary(records); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return fmt.Errorf("saved query library exceeds 32 MiB")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "queries-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func validQueryName(name string) error {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") || len(name) > 200 {
		return fmt.Errorf("enter a query name of 1–200 bytes")
	}
	return nil
}

func (a *App) loadQueries() {
	w := a.ensureWorkspace()
	w.queryStoreBusy = true
	go func() {
		path, err := queryLibraryPath()
		var records []savedQuery
		if err == nil {
			records, err = readQueryLibrary(path)
		}
		a.win.Update(func() {
			if a.closed {
				return
			}
			w.queryStoreBusy = false
			w.queriesLoaded = true
			if err != nil {
				a.status = "Could not load saved queries: " + err.Error()
				return
			}
			w.queries = records
			a.ensureQueryOwners()
		})
	}()
}

func (a *App) ensureQueryOwners() {
	w := a.ensureWorkspace()
	for _, record := range w.queries {
		found := false
		for _, owner := range w.sessions {
			if owner.workspaceName() == record.Connection {
				found = true
				break
			}
		}
		if !found {
			owner := &App{connectionWorkspace: w, win: a.win, queryWorkspace: record.Connection, explorerWidth: a.explorerWidth, editorPercent: a.editorPercent, hideResults: true, showFiles: true, selected: -1, connections: a.connections}
			w.sessions = append(w.sessions, owner)
		}
	}
	for _, owner := range w.sessions {
		if owner.client != nil || owner.queryWorkspace == "" {
			continue
		}
		for _, saved := range a.connections {
			if saved.Name == owner.queryWorkspace {
				copy := saved
				owner.lastConnection = &copy
				break
			}
		}
	}
}

func (a *App) queryRecords() []savedQuery {
	var records []savedQuery
	for _, q := range a.ensureWorkspace().queries {
		if q.Connection == a.workspaceName() {
			records = append(records, q)
		}
	}
	return records
}

func (a *App) updateQueries(records []savedQuery, done func()) {
	w := a.ensureWorkspace()
	if w.queryStoreBusy || a.win == nil {
		return
	}
	w.queryStoreBusy = true
	go func() {
		path, err := queryLibraryPath()
		if err == nil {
			err = writeQueryLibrary(path, records)
		}
		a.win.Update(func() {
			if a.closed {
				return
			}
			w.queryStoreBusy = false
			if err != nil {
				a.queryName.errorText = err.Error()
				a.status = "Could not save query: " + err.Error()
				return
			}
			w.queries = records
			if done != nil {
				done()
			}
		})
	}()
}

func (a *App) saveQueryDialog(tab *queryTab) {
	if tab == nil || tab.tableView != nil {
		return
	}
	a.activateConnection()
	a.queryName = queryNameDialog{tab: tab, savedID: tab.savedID, name: tab.name}
	if tab.savedID != "" {
		a.commitQueryName()
		return
	}
	a.showQueryName = true
}

func (a *App) renameSavedQuery(record savedQuery) {
	a.activateConnection()
	a.queryName = queryNameDialog{savedID: record.ID, name: record.Name}
	a.showQueryName = true
}

func (a *App) commitQueryName() {
	draft := a.queryName
	draft.name = strings.TrimSpace(draft.name)
	if err := validQueryName(draft.name); err != nil {
		a.queryName.errorText = err.Error()
		return
	}
	w := a.ensureWorkspace()
	records := append([]savedQuery(nil), w.queries...)
	for _, q := range records {
		if q.Connection == a.workspaceName() && q.Name == draft.name && q.ID != draft.savedID {
			a.queryName.errorText = "A saved query already has this name"
			return
		}
	}
	id := draft.savedID
	if id == "" {
		id = rand.Text()
	}
	found := false
	for i := range records {
		if records[i].ID == id {
			records[i].Name = draft.name
			if draft.tab != nil {
				records[i].SQL = draft.tab.state.Buffer.String()
			}
			found = true
			break
		}
	}
	if !found {
		if draft.tab == nil {
			a.queryName.errorText = "Saved query no longer exists"
			return
		}
		records = append(records, savedQuery{ID: id, Name: draft.name, SQL: draft.tab.state.Buffer.String(), Connection: a.workspaceName()})
	}
	a.updateQueries(records, func() {
		for _, owner := range w.sessions {
			for _, tab := range owner.tabs {
				if tab.savedID == id || tab == draft.tab {
					tab.savedID, tab.name = id, draft.name
				}
			}
		}
		a.showQueryName = false
		a.status = "Query saved · " + draft.name
	})
}

func (a *App) removeSavedQuery(record savedQuery) {
	var records []savedQuery
	for _, q := range a.ensureWorkspace().queries {
		if q.ID != record.ID {
			records = append(records, q)
		}
	}
	a.updateQueries(records, func() {
		for _, owner := range a.connectionSessions() {
			for _, tab := range owner.tabs {
				if tab.savedID == record.ID {
					tab.savedID = ""
				}
			}
		}
		a.status = "Saved query removed · " + record.Name
	})
}

func (a *App) openSavedQuery(record savedQuery) {
	for i, tab := range a.tabs {
		if tab.savedID == record.ID {
			a.selectQueryTab(i)
			return
		}
	}
	a.activateConnection()
	a.addQuery(record.SQL)
	tab := a.currentEditor()
	tab.name, tab.savedID = record.Name, record.ID
}

func (a *App) queryNameModal(c *ui.Context, k palette) {
	ui.Modal(c.Key("query-name-dialog"), &a.showQueryName, func() {
		ui.Column(c).Width(340).Gap(12).Children(func() {
			title := "Save query"
			if a.queryName.tab == nil {
				title = "Rename saved query"
			}
			ui.Text(c, title).FontSize(17).FontWeight(600).TextColor(k.text)
			connectionField(c, k, "Query name", &a.queryName.name, false)
			if a.queryName.errorText != "" {
				ui.Text(c, a.queryName.errorText).FontSize(11).TextColor(k.danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				busy := a.ensureWorkspace().queryStoreBusy
				if ui.Button(c, "Cancel").Disabled(busy).Clicked() {
					a.showQueryName = false
				}
				if ui.PrimaryButton(c, "Save").Label("Save query name").Disabled(busy).Clicked() {
					a.commitQueryName()
				}
			})
		})
	})
}

func readSQLFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSQLFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSQLFileBytes {
		return "", fmt.Errorf("SQL files must be at most 4 MiB")
	}
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", fmt.Errorf("SQL files must contain UTF-8 text without NUL")
	}
	return string(data), nil
}

func (a *App) openSQLFile() {
	if a.win == nil {
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "Open SQL file", Filters: []mygo.FileFilter{{Name: "SQL files", Extensions: []string{"sql"}}, {Name: "All files", Extensions: []string{"*"}}}})
	if err != nil {
		a.status = err.Error()
		return
	}
	if len(paths) == 0 {
		return
	}
	path := paths[0]
	go func() {
		sql, err := readSQLFile(path)
		a.win.Update(func() {
			if a.closed {
				return
			}
			if err != nil {
				a.status = "Could not open SQL file: " + err.Error()
				return
			}
			a.activateConnection()
			a.addQuery(sql)
			a.currentEditor().name = filepath.Base(path)
			a.status = "Opened SQL file · " + filepath.Base(path)
		})
	}()
}
