package app

import (
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo/ui"
)

type catalogInspection struct {
	Scope     string
	Group     string
	Object    database.Definition
	FetchedAt time.Time
	Error     string
}

func nativeProperty(properties []database.Property, name string) string {
	for _, p := range properties {
		if p.Name == name {
			return p.Value
		}
		if value := nativeProperty(p.Children, name); value != "" {
			return value
		}
	}
	return ""
}

func sequenceDataType(d database.Definition) string { return nativeProperty(d.Properties, "data_type") }

func (a *App) inspectCatalogObject(scope string, d database.Definition, groups ...string) {
	group := d.Kind
	if len(groups) > 0 {
		group = groups[0]
	}
	if group == "" {
		group = "sequences"
	}
	a.inspectorColumn = nil
	a.inspectorDefinition = nil
	a.setObjectPath(append(a.extraObjectPath(scope), group, d.Name)...)
	a.inspectorObject = &catalogInspection{Scope: scope, Group: group, Object: d, FetchedAt: a.explorerCatalog.FetchedAt}
	a.activeTable = ""
	a.inspectorTable = database.Table{}
	a.openInspector()
	a.selectedTree = "extra:" + scope + ":" + group + ":" + d.Name
}

func (a *App) refreshCatalogInspection() {
	selected := a.inspectorObject
	if selected == nil {
		return
	}
	for _, g := range a.explorerCatalog.Groups {
		if g.Name != selected.Group || (g.Scope != selected.Scope && !(g.Scope == "schema" && strings.HasPrefix(selected.Scope, "schema:"))) {
			continue
		}
		if g.State == "unavailable" {
			selected.Error = g.Message
			return
		}
		for _, d := range g.Items {
			if d.Name == selected.Object.Name {
				selected.Object = d
				selected.FetchedAt = a.explorerCatalog.FetchedAt
				selected.Error = g.Message
				return
			}
		}
	}
	a.inspectorObject = nil
	a.selectedTree = "extra:" + selected.Scope + ":" + selected.Group
	a.setObjectPath(append(a.extraObjectPath(selected.Scope), selected.Group)...)
	a.status = catalogObjectKind(selected.Group) + " no longer listed in refreshed catalog"
}

func (a *App) catalogInspector(c *ui.Context, k palette) {
	selected := a.inspectorObject
	if selected.Group != "sequences" {
		a.catalogObjectInspector(c, k)
		return
	}
	d := selected.Object
	schema := strings.TrimPrefix(selected.Scope, "schema:")
	inspectorHeading(c, k, d.Name, "Sequence", inspectorContext(schema, sequenceDataType(d)), "sequence", "Inspected sequence "+d.Name, func() {
		if toolbarAction(c, k, "refresh", "Refresh sequence metadata", k.muted).Disabled(a.client == nil || a.explorerLoading).Clicked() {
			a.loadExplorerCatalog(true)
		}
	})
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Padding(12).Gap(8).Children(func() {
			if selected.Error != "" {
				ui.Text(c, selected.Error).TextColor(k.danger)
			}
			if !selected.FetchedAt.IsZero() {
				ui.Text(c, "Metadata · "+selected.FetchedAt.Local().Format("02 Jan 15:04")).FontSize(10).TextColor(k.muted)
			}
			fields := []struct{ label, value string }{{"Data type", sequenceDataType(d)}, {"Schema", schema}, {"Owner", nativeProperty(d.Properties, "owner")}, {"Start value", nativeProperty(d.Properties, "seqstart")}, {"Increment", nativeProperty(d.Properties, "seqincrement")}, {"Minimum", nativeProperty(d.Properties, "seqmin")}, {"Maximum", nativeProperty(d.Properties, "seqmax")}, {"Cache", nativeProperty(d.Properties, "seqcache")}, {"Cycle", nativeProperty(d.Properties, "seqcycle")}}
			for _, field := range fields {
				if field.value == "" {
					continue
				}
				inspectorField(c, k, field.label, field.value)
			}
			a.propertyTree(c, k, []database.Property{{Name: "Native metadata", Children: d.Properties}}, "inspector:sequence:"+selected.Scope+":"+d.Name, 0, 0)
		})
	})
}

func catalogObjectKind(group string) string {
	if title, ok := map[string]string{"access methods": "Access method", "casts": "Cast", "extensions": "Extension", "languages": "Language", "roles": "Role", "tablespaces": "Tablespace", "sequences": "Sequence", "routines": "Routine", "aggregates": "Aggregate", "object types": "Object type", "virtual views": "Virtual view"}[group]; ok {
		return title
	}
	return group
}

func (a *App) catalogObjectInspector(c *ui.Context, k palette) {
	selected := a.inspectorObject
	d := selected.Object
	context := inspectorContext(a.explorerCatalog.Database, "Database Objects")
	if selected.Scope == "server" {
		context = "Server Objects"
	} else if strings.HasPrefix(selected.Scope, "schema:") {
		context = inspectorContext(a.explorerCatalog.Database, strings.TrimPrefix(selected.Scope, "schema:"))
	} else if selected.Scope == "virtual" {
		context = "Virtual views"
	}
	inspectorHeading(c, k, d.Name, catalogObjectKind(selected.Group), context, "catalog-object", "Inspected catalog object "+d.Name, func() {
		if toolbarAction(c, k, "refresh", "Refresh catalog object metadata", k.muted).Disabled(a.client == nil || a.explorerLoading).Clicked() {
			a.loadExplorerCatalog(true)
		}
	})
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Padding(12).Gap(10).Children(func() {
			if selected.Error != "" {
				ui.Text(c, selected.Error).TextColor(k.danger)
			}
			if a.explorerError != "" {
				ui.Text(c, a.explorerError).TextColor(k.danger)
			}
			if a.explorerLoading {
				ui.Text(c, "Refreshing metadata…").FontSize(11).TextColor(k.muted)
			}
			if !selected.FetchedAt.IsZero() {
				ui.Text(c, "Metadata · "+selected.FetchedAt.Local().Format("02 Jan 15:04")).FontSize(10).TextColor(k.muted)
			}
			if d.SQL != "" {
				inspectorCode(c, k, "Definition", d.SQL)
				if ui.Button(c, "Open SQL in editor").Clicked() {
					a.addQuery(d.SQL)
				}
			}
			a.propertyTree(c, k, d.Properties, "inspector:catalog:"+selected.Scope+":"+selected.Group+":"+d.Name, 0, 0)
		})
	})
}
