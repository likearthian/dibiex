package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

// The menu anchor stays mounted so moving into its popup does not lose the
// pending menu action. Only its glyph and hover background are conditional.
func objectActions(c *ui.Context, k palette, row ui.Element, label string, build func(*ui.Menu)) {
	button := ui.ButtonBase(c.Key("object-actions:"+label)).Size(24, 24).Shrink(0).Radius(4).Center().Label("Actions for " + label).Tooltip("Actions for " + label)
	button.PopoverMenuButton(build)
	if row.Hovered() || button.Focused() {
		if button.Hovered() {
			button.Background(k.selected)
		}
		button.Children(func() { ui.Icon(c, controlIcon("ellipsis")).Size(16, 16).TextColor(k.muted) })
	}
	row.PopoverMenu(build)
}

func (a *App) editConnection(name string, cfg database.Config) {
	if a.busy && a.hasConnection() {
		a.newConnectionSession().editConnection(name, cfg)
		return
	}
	a.activateConnection()
	if a.busy {
		return
	}
	maxBytes := cfg.MaxBytesBilled
	if maxBytes <= 0 {
		maxBytes = 1073741824
	}
	a.connectionForm = connectionForm{name: name, originalName: name, engine: string(cfg.Engine), config: cfg, maxBytes: strconv.FormatInt(maxBytes, 10)}
	a.connectionForm.config.Password = ""
	if credential, ok := a.credential(name, cfg); ok {
		a.connectionForm.passwordMode = credential.Mode
	}
	a.showConnection = true
}

func (a *App) matchingConnection() (savedConnection, bool) {
	if !a.hasConnection() {
		return savedConnection{}, false
	}
	cfg := a.client.Settings()
	for _, p := range a.connections {
		if p.Name == a.connection && sameConnection(p.Config, cfg) {
			return p, true
		}
	}
	for _, p := range a.connections {
		if sameConnection(p.Config, cfg) {
			return p, true
		}
	}
	return savedConnection{}, false
}

func sameConnection(a, b database.Config) bool {
	if a.Engine != b.Engine {
		return false
	}
	switch a.Engine {
	case database.SQLite:
		return a.Path == b.Path
	case database.PostgreSQL, database.MySQL:
		return a.Host == b.Host && a.Port == b.Port && a.Database == b.Database && a.User == b.User && a.SSLMode == b.SSLMode
	case database.BigQuery:
		return a.Project == b.Project && a.DatasetProject == b.DatasetProject && a.Dataset == b.Dataset && a.Location == b.Location && a.MaxBytesBilled == b.MaxBytesBilled && a.BigQueryAuthMethod() == b.BigQueryAuthMethod() && a.CredentialFile == b.CredentialFile
	}
	return false
}

func (a *App) sqlDraft(name, sql string) {
	a.activateConnection()
	a.addQuery(sql)
	a.tabs[a.activeTab].name = name + ".sql"
	a.hideResults = false
	a.status = "Review the SQL, then use Execute writes to apply it"
}

func schemaReference(s database.Schema, engine database.Engine) string {
	if engine == database.BigQuery {
		return "`" + escapeBigQuery(s.Project+"."+s.Name) + "`"
	}
	return database.QuoteIdentifierFor(engine, s.Name)
}

func escapeBigQuery(s string) string {
	// Table.Reference owns the same engine-specific escaping for table paths.
	return strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(s)
}

func (a *App) schemaMenu(m *ui.Menu, s database.Schema, index int) {
	engine := a.client.Engine()
	if m.Item("Add table…").Disabled(a.busy).Chosen() {
		if engine == database.PostgreSQL || engine == database.SQLite {
			a.openTableDesigner(s.Name)
			return
		}
		t := database.Table{Project: s.Project, Schema: s.Name, Name: "new_table"}
		typ := "INTEGER"
		if engine == database.BigQuery {
			typ = "INT64"
		}
		a.sqlDraft("add-table", "CREATE TABLE "+t.Reference(engine)+" (\n  id "+typ+"\n);")
	}
	if m.Item("Modify schema…").Disabled(a.busy || engine != database.PostgreSQL).Chosen() {
		a.sqlDraft("rename-schema", "ALTER SCHEMA "+schemaReference(s, engine)+" RENAME TO "+database.QuoteIdentifier("new_schema")+";")
	}
	if m.Item("Delete schema…").Disabled(a.busy || engine == database.SQLite).Chosen() {
		a.sqlDraft("drop-schema", "DROP SCHEMA "+schemaReference(s, engine)+";")
	}
	m.Separator()
	if m.Item("Refresh").Disabled(a.busy || a.client == nil || a.schemaIsLoading(s.ID())).Chosen() {
		a.invalidateMetadata()
		a.loadSchema(index, true)
	}
}

func (a *App) tableMenu(m *ui.Menu, t database.Table) {
	engine := a.client.Engine()
	if engine == database.PostgreSQL && (t.Kind == "" || t.Kind == "Tables" || t.Kind == "Partitioned tables") {
		a.postgresTableMenu(m, t)
		return
	}
	kind := "TABLE"
	if t.Kind == "Views" {
		kind = "VIEW"
	}
	if t.Kind == "Materialized views" {
		kind = "MATERIALIZED VIEW"
	}
	if t.Kind == "Foreign tables" {
		kind = "FOREIGN TABLE"
	}
	ref := t.Reference(engine)
	if m.Item("Add column…").Disabled(a.busy || (t.Kind != "" && t.Kind != "Tables" && t.Kind != "Partitioned tables")).Chosen() {
		typ := "TEXT"
		col := database.QuoteIdentifierFor(engine, "new_column")
		if engine == database.BigQuery {
			typ = "STRING"
			col = "`new_column`"
		}
		a.sqlDraft("add-column", "ALTER TABLE "+ref+" ADD COLUMN "+col+" "+typ+";")
	}
	if m.Item("Modify / rename…").Disabled(a.busy || (engine == database.SQLite && kind != "TABLE") || (engine == database.BigQuery && kind != "TABLE") || t.Kind == "External tables").Chosen() {
		name := database.QuoteIdentifierFor(engine, "new_name")
		if engine == database.BigQuery {
			name = "`new_name`"
		}
		if engine == database.MySQL && kind != "TABLE" {
			a.sqlDraft("rename-object", "RENAME TABLE "+ref+" TO "+database.QuoteIdentifierFor(engine, t.Schema)+"."+name+";")
		} else {
			a.sqlDraft("rename-object", "ALTER "+kind+" "+ref+" RENAME TO "+name+";")
		}
	}
	if m.Item("Delete…").Disabled(a.busy).Chosen() {
		a.sqlDraft("drop-object", "DROP "+kind+" "+ref+";")
	}
	m.Separator()
	if m.Item("Refresh").Disabled(a.busy || a.client == nil).Chosen() {
		a.invalidateMetadata()
		a.inspectTable(t, true)
	}
}

func (a *App) invalidateMetadata() {
	a.metadataVersion++
	a.explorerLoading = false
	a.metadata, a.metadataErrors, a.metadataLoading = nil, nil, nil
	a.tableSectionLoading, a.tableSectionErrors = nil, nil
}

func (a *App) refreshCatalog(after ...func()) {
	if a.busy || a.closed || a.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.connectionContext(), 30*time.Second)
	a.busy, a.cancel, a.status = true, cancel, "Refreshing database objects…"
	a.started = time.Time{}
	client, win := a.client, a.win
	projects := a.connectionSettings().BigQueryProjects()
	runner, pipeline := a.catalogRunner()
	selectedTab := a.currentEditor()
	go func() {
		defer cancel()
		var schemas []database.Schema
		var err error
		if client.Engine() == database.BigQuery && pipeline != nil {
			schemas, err = pipeline.BigQueryCatalog(ctx, projects, true)
		} else if client.Engine() == database.BigQuery {
			for _, project := range projects {
				var items []database.Schema
				items, err = runner.BigQueryProjectDatasets(ctx, project)
				if err != nil {
					break
				}
				schemas = append(schemas, items...)
			}
		} else if pipeline != nil {
			schemas, err = pipeline.Catalog(ctx, true)
		} else {
			schemas, err = runner.Catalog(ctx)
		}
		warning := ""
		if pipeline != nil {
			warning = pipeline.Warning()
		}
		win.Update(func() {
			if a.closed || a.client != client {
				return
			}
			a.busy, a.cancel = false, nil
			if client.Engine() == database.SQLite && a.currentEditor() != selectedTab {
				a.loadQueryView(a.currentEditor())
				return
			}
			a.cacheWarning = warning
			if err != nil {
				a.status = "Refresh failed: " + err.Error()
				return
			}
			a.applyCatalog(schemas, false)
			a.invalidateMetadata()
			a.loadExplorerCatalog(true)
			a.status = "Database objects refreshed"
			for _, callback := range after {
				callback()
			}
		})
	}()
}
