package app

import (
	"context"
	"fmt"
	"time"

	"dbcraft/internal/database"
)

func cloneTableDefinition(d database.PostgreSQLTable) database.PostgreSQLTable {
	d.Columns = append([]database.PostgreSQLColumn(nil), d.Columns...)
	d.Constraints = append([]database.PostgreSQLConstraint(nil), d.Constraints...)
	d.Indexes = append([]database.PostgreSQLIndex(nil), d.Indexes...)
	return d
}

func (f *tableDesigner) resetEdit() {
	f.definition = cloneTableDefinition(f.edit.Definition)
	f.owner = f.definition.Owner
	f.tablespace = f.definition.Tablespace
	if f.tablespace == "" {
		f.tablespace = "Database default"
	}
	f.columnIDs, f.constraintIDs, f.indexIDs = nil, nil, nil
	for range f.definition.Columns {
		f.nextID++
		f.columnIDs = append(f.columnIDs, f.nextID)
	}
	for range f.definition.Constraints {
		f.nextID++
		f.constraintIDs = append(f.constraintIDs, f.nextID)
	}
	for range f.definition.Indexes {
		f.nextID++
		f.indexIDs = append(f.indexIDs, f.nextID)
	}
	f.errorText, f.confirmDrops = "", false
}

func (f *tableDesigner) statements() ([]string, bool, error) {
	if f.edit == nil {
		s, err := f.table().Statements()
		return s, false, err
	}
	if !f.loaded {
		return nil, false, fmt.Errorf("load the current table definition before saving")
	}
	changes, err := f.edit.Changes(f.table())
	return changes.Statements, changes.Destructive, err
}

func (f *tableDesigner) originalColumn(number int) *database.PostgreSQLColumnEdit {
	if f.edit == nil || number == 0 {
		return nil
	}
	for i := range f.edit.Columns {
		if f.edit.Columns[i].Number == number {
			return &f.edit.Columns[i]
		}
	}
	return nil
}

func (a *App) openTableEditDesigner(table database.Table) {
	if a.client == nil || !editablePostgreSQLTable(a.client.Engine(), table) {
		return
	}
	a.activateConnection()
	f := &tableDesigner{edit: &database.PostgreSQLTableDesign{Table: table}, tab: "General", loading: true, owner: "", tablespace: "Database default", tablespaces: []string{"Database default"}}
	a.tableDesigner, a.sqliteDesigner, a.showCreateTable = f, nil, true
	a.showTableAction = false
	a.loadTableEditDesigner(f)
}

func (a *App) loadTableEditDesigner(f *tableDesigner) {
	if a.win == nil {
		return
	}
	f.loading, f.loaded, f.errorText = true, false, ""
	client, win, table := a.client, a.win, f.edit.Table
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	f.cancel = cancel
	go func() {
		defer cancel()
		snapshot, err := client.LoadPostgreSQLTableDesign(ctx, table)
		win.Update(func() {
			if a.closed || a.client != client || a.tableDesigner != f {
				return
			}
			f.loading, f.cancel = false, nil
			if err != nil {
				f.errorText = err.Error()
				return
			}
			f.edit, f.loaded = &snapshot, true
			f.resetEdit()
			f.owners = []string{snapshot.Definition.Owner}
			f.tablespaces = []string{"Database default"}
			if snapshot.Definition.Tablespace != "" {
				f.tablespaces = append(f.tablespaces, snapshot.Definition.Tablespace)
			}
			f.schemas = []string{table.Schema}
			for _, schema := range a.schemas {
				if schema.Name != table.Schema {
					f.schemas = append(f.schemas, schema.Name)
				}
			}
			a.loadTableDesignerChoices(f)
		})
	}()
}

func (a *App) saveEditedTable(f *tableDesigner) {
	if f.edit == nil || !f.loaded || f.saving || a.client == nil || a.win == nil {
		return
	}
	statements, destructive, err := f.statements()
	if err != nil {
		f.errorText = err.Error()
		return
	}
	if destructive && !f.confirmDrops {
		f.errorText = "Confirm the removals shown in the SQL preview"
		return
	}
	if len(statements) == 0 {
		f.info = "No changes to save"
		return
	}
	definition, original := cloneTableDefinition(f.table()), *f.edit
	ctx, cancel := context.WithTimeout(a.connectionContext(), 5*time.Minute)
	f.saving, f.errorText, f.cancel = true, "", cancel
	client, win, pipeline := a.client, a.win, a.introspector
	go func() {
		defer cancel()
		session, err := client.NewQuerySession(ctx)
		if err == nil {
			err = session.ApplyPostgreSQLTableDesign(ctx, original, definition)
			session.Close()
		}
		var schemas []database.Schema
		var refreshErr error
		if err == nil {
			if pipeline != nil {
				pipeline.Invalidate()
				schemas, refreshErr = pipeline.Catalog(ctx, true)
			} else {
				schemas, refreshErr = client.Catalog(ctx)
			}
		}
		win.Update(func() {
			if a.closed || a.client != client || a.tableDesigner != f {
				return
			}
			f.saving, f.cancel = false, nil
			if err != nil {
				f.errorText = err.Error()
				return
			}
			a.showCreateTable = false
			a.invalidateMetadata()
			after := original.Table
			after.Schema, after.Name = definition.Schema, definition.Name
			oldColumn, newColumn := "", ""
			if selected := a.inspectorColumn; selected != nil && selected.Table.ID() == original.Table.ID() && len(selected.Path) == 1 {
				for _, column := range definition.Columns {
					if prior := f.originalColumn(column.Number); prior != nil && prior.Name == selected.Path[0] {
						oldColumn, newColumn = prior.Name, column.Name
					}
				}
			}
			a.editedTableSelection(original.Table, after, oldColumn, newColumn)
			if refreshErr == nil {
				a.applyCatalog(schemas, false)
			}
			if a.inspectorTable.ID() == after.ID() {
				a.loadTableMetadata(after, true)
			}
			a.status = "Updated table · " + after.Label()
			if refreshErr != nil {
				a.status += " · catalog refresh failed: " + refreshErr.Error()
			}
			a.loadExplorerCatalog(true)
		})
	}()
}
