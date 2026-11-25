package app

import (
	"math"
	"slices"
	"strings"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

func (a *App) connectionObjectPath() []string {
	path := []string{"Database"}
	if a.hasConnection() {
		path = append(path, a.connectionLabel())
	}
	return path
}
func (a *App) databaseObjectPath() []string {
	path := a.connectionObjectPath()
	if a.explorerEngine() == database.BigQuery {
		return append(path, a.bigQueryProject())
	}
	if a.explorerEngine() == database.PostgreSQL {
		name := a.explorerCatalog.Database
		if name == "" {
			name = a.client.Scope()
		}
		if name != "" {
			path = append(path, name)
		}
	}
	return path
}
func (a *App) tableObjectPath(t database.Table) []string {
	path := a.databaseObjectPath()
	if a.explorerEngine() == database.BigQuery {
		path = append(a.connectionObjectPath(), t.Project)
	}
	if t.Schema != "" {
		path = append(path, t.Schema)
	}
	if a.explorerEngine() == database.BigQuery {
		return append(path, t.Name)
	}
	return append(path, strings.ToLower(tableGroup(t)), t.Name)
}
func (a *App) extraObjectPath(scope string) []string {
	path := a.databaseObjectPath()
	switch {
	case strings.HasPrefix(scope, "schema:"):
		return append(path, strings.TrimPrefix(scope, "schema:"))
	case scope == "server":
		return append(a.connectionObjectPath(), "Server Objects")
	case scope == "database":
		return append(path, "Database Objects")
	}
	return path
}
func (a *App) setObjectPath(path ...string) {
	if slices.Equal(a.objectPath, path) {
		return
	}
	a.objectPath = append([]string(nil), path...)
	a.breadcrumbScroll.X = math.MaxFloat32
}
func (a *App) selectTreePath(key, label string) {
	switch {
	case key == "connection":
		a.setObjectPath(a.connectionObjectPath()...)
	case strings.HasPrefix(key, "project:"):
		a.setObjectPath(append(a.connectionObjectPath(), label)...)
	case strings.HasPrefix(key, "database:"):
		a.setObjectPath(a.databaseObjectPath()...)
	case key == "catalog-databases":
		a.setObjectPath(append(a.connectionObjectPath(), label)...)
	case strings.HasPrefix(key, "schema:"):
		if a.explorerEngine() == database.BigQuery {
			for _, schema := range a.schemas {
				if key == "schema:"+schema.ID() {
					a.setObjectPath(append(a.connectionObjectPath(), schema.Project, schema.Name)...)
					return
				}
			}
		}
		a.setObjectPath(append(a.databaseObjectPath(), label)...)
	case strings.HasPrefix(key, "group:"):
		for _, s := range a.schemas {
			if key == "group:"+s.ID()+":"+label {
				a.setObjectPath(append(a.databaseObjectPath(), s.Name, strings.ToLower(label))...)
				return
			}
		}
	case strings.HasPrefix(key, "extra-root:"):
		a.setObjectPath(a.extraObjectPath(strings.TrimPrefix(key, "extra-root:"))...)
	case strings.HasPrefix(key, "extra:"):
		scope := strings.TrimSuffix(strings.TrimPrefix(key, "extra:"), ":"+label)
		a.setObjectPath(append(a.extraObjectPath(scope), label)...)
	}
}
func (a *App) objectBreadcrumb(c *ui.Context, k palette) {
	width, _ := c.Size()
	if width != a.breadcrumbWindowWidth {
		a.breadcrumbWindowWidth = width
		a.breadcrumbScroll.X = math.MaxFloat32
	}
	path := a.objectPath
	if len(path) == 0 {
		path = a.connectionObjectPath()
	}
	ui.ScrollHorizontal(c.Key("object-breadcrumb")).TrackScroll(&a.breadcrumbScroll).Grow(1).MinWidth(0).Height(28).Label("Object breadcrumb").Tooltip(strings.Join(path, " › ")).Children(func() {
		ui.Row(c).Height(28).Gap(8).AlignItems(ui.Center).Children(func() {
			for i, part := range path {
				if i > 0 {
					ui.Icon(c, controlIcon("chevron-right")).Size(12, 12).Shrink(0).TextColor(k.muted)
				}
				if i == len(path)-1 && a.inspectorColumn != nil && a.selectedTree == columnTreeKey(a.inspectorColumn.Table, a.inspectorColumn.Path) {
					ui.Icon(c, controlIcon(columnTypeIcon(a.inspectorColumn.Column))).Size(14, 14).Shrink(0).TextColor(k.muted)
				}
				ui.Text(c, part).SingleLine().Shrink(0).FontSize(11).TextColor(k.muted)
			}
		})
	})
}
