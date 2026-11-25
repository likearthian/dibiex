package app

import (
	"dibiex/internal/database"
	"github.com/egoist/mygo/ui"
)

func databaseIcon(c *ui.Context, k palette, engine database.Engine) {
	name, color := "database", k.muted
	switch engine {
	case database.SQLite:
		name, color = "brand-sqlite", ui.Hex("#0f80cc")
		if c.Theme().Dark {
			color = ui.Hex("#74c2e8")
		}
	case database.PostgreSQL:
		name, color = "brand-postgresql", ui.Hex("#336791")
		if c.Theme().Dark {
			color = ui.Hex("#75acd3")
		}
	case database.MySQL:
		name, color = "brand-mysql", ui.Hex("#00758f")
		if c.Theme().Dark {
			color = ui.Hex("#5db6cc")
		}
	case database.BigQuery:
		name, color = "brand-googlebigquery", ui.Hex("#4285f4")
	}
	ui.Icon(c, controlIcon(name)).Size(16, 16).Shrink(0).TextColor(color).Label(string(engine)).Tooltip(string(engine))
}
