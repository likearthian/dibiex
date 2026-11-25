package app

import (
	"fmt"

	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func containsDesignerChoice(choices []string, value string) bool {
	for _, choice := range choices {
		if choice == value {
			return true
		}
	}
	return false
}

func (a *App) tableDesignerIndexes(c *ui.Context, k palette, f *tableDesigner) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Table indexes").FontWeight(600).TextColor(k.text)
		ui.Spacer(c)
		if ui.Button(c, "+ Add index").Label("Add table index").Clicked() {
			f.nextID++
			f.indexIDs = append(f.indexIDs, f.nextID)
			f.definition.Indexes = append(f.definition.Indexes, database.PostgreSQLIndex{Method: "btree"})
		}
	})
	ui.Text(c, "Simple column indexes can be edited here. Constraint-owned indexes are managed in Constraints; advanced native definitions are preserved.").FontSize(11).TextColor(k.muted)
	remove := -1
	for i := range f.definition.Indexes {
		index := &f.definition.Indexes[i]
		readonly := index.ReadOnlyReason != ""
		ui.Column(c.Key(fmt.Sprintf("index:%d", f.indexIDs[i]))).FillWidth().MinWidth(0).Padding(12).Gap(10).Radius(6).Border(1, k.border).Background(k.panel).Children(func() {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				ui.TextInput(c.Key("name"), &index.Name).Grow(1).MinWidth(0).Placeholder("Index name").Label(fmt.Sprintf("Index %d name", i+1)).Disabled(readonly)
				ui.Select(c.Key("method"), &index.Method, []string{"btree", "hash", "gin", "gist", "spgist", "brin"}).Width(150).Label(fmt.Sprintf("Index %d method", i+1)).Disabled(readonly)
				if toolbarAction(c, k, "x", fmt.Sprintf("Remove index %d", i+1), k.muted).Disabled(index.ConstraintOwned).Clicked() {
					remove = i
				}
			})
			if readonly {
				ui.Text(c, index.ReadOnlyReason).FontSize(11).TextColor(k.muted)
				inspectorCode(c, k, "Native index SQL", index.SQL)
				return
			}
			ui.TextInput(c.Key("columns"), &index.Columns).FillWidth().Placeholder("Column names separated by commas").Label(fmt.Sprintf("Index %d columns", i+1))
			ui.Checkbox(c, &index.Unique, "Unique index").Label(fmt.Sprintf("Index %d unique", i+1))
			ui.TextInput(c.Key("predicate"), &index.Predicate).FillWidth().Placeholder("WHERE expression (optional)").Label(fmt.Sprintf("Index %d predicate", i+1))
		})
	}
	if remove >= 0 {
		f.definition.Indexes = append(f.definition.Indexes[:remove], f.definition.Indexes[remove+1:]...)
		f.indexIDs = append(f.indexIDs[:remove], f.indexIDs[remove+1:]...)
	}
}
