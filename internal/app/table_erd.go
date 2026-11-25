package app

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type tableERD struct {
	table     database.Table
	graph     database.TableDiagram
	loading   bool
	errorText string
}
type erdNode struct {
	x, y, w, h float32
	table      database.DiagramTable
}

func layoutTableERD(graph database.TableDiagram) ([]erdNode, float32, float32) {
	var nodes []erdNode
	y := float32(40)
	for i, table := range graph.Tables {
		h := float32(48 + 20*len(table.Columns))
		h = float32((int(h) + 119) / 120 * 120)
		x := float32(400)
		if i == 0 {
			x = 40
		} else {
			if i > 1 {
				y = nodes[len(nodes)-1].y + nodes[len(nodes)-1].h + 120
			}
		}
		nodes = append(nodes, erdNode{x, y, 280, h, table})
	}
	height := float32(300)
	for _, node := range nodes {
		height = max(height, node.y+node.h+40)
	}
	if len(nodes) > 1 {
		nodes[0].y = 40 + float32(max(0, int((height-nodes[0].h)/2-40))/120*120)
	}
	return nodes, 740, height
}

func escapedXML(value string) string {
	var out bytes.Buffer
	xml.EscapeText(&out, []byte(value))
	return out.String()
}
func tableERDDrawio(graph database.TableDiagram) string {
	nodes, _, _ := layoutTableERD(graph)
	ids := map[string]string{}
	var out strings.Builder
	out.WriteString(`<mxGraphModel adaptiveColors="auto"><root><mxCell id="0"/><mxCell id="1" parent="0"/>`)
	for i, node := range nodes {
		id := fmt.Sprintf("table-%d", i)
		ids[node.table.Table.ID()] = id
		label := node.table.Table.Label()
		for _, col := range node.table.Columns {
			label += "\n"
			if col.PrimaryKey {
				label += "PK "
			}
			label += col.Name + "  " + col.Type
		}
		fmt.Fprintf(&out, `<mxCell id="%s" value="%s" style="rounded=1;whiteSpace=wrap;html=0;align=left;verticalAlign=top;spacing=12;fontFamily=Inter;fontSize=12;" vertex="1" parent="1"><mxGeometry x="%.0f" y="%.0f" width="%.0f" height="%.0f" as="geometry"/></mxCell>`, id, escapedXML(label), node.x, node.y, node.w, node.h)
	}
	for i, relation := range graph.Relations {
		fmt.Fprintf(&out, `<mxCell id="fk-%d" value="%s" style="edgeStyle=orthogonalEdgeStyle;rounded=1;html=0;endArrow=open;labelBackgroundColor=page;" edge="1" parent="1" source="%s" target="%s"><mxGeometry relative="1" as="geometry"/></mxCell>`, i, escapedXML(relation.Name), ids[relation.Source.ID()], ids[relation.Target.ID()])
	}
	out.WriteString(`</root></mxGraphModel>`)
	return out.String()
}

func (a *App) openTableERD(t database.Table) {
	if a.client == nil || a.win == nil {
		return
	}
	a.activateConnection()
	f := &tableERD{table: t, loading: true}
	a.tableERD, a.showTableERD = f, true
	client, win, parent := a.client, a.win, a.connectionContext()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		graph, err := client.PostgreSQLDiagram(ctx, t)
		win.Update(func() {
			if a.closed || a.client != client || a.tableERD != f {
				return
			}
			f.loading = false
			if err != nil {
				f.errorText = err.Error()
				return
			}
			f.graph = graph
		})
	}()
}

func (a *App) exportTableERD(f *tableERD) {
	if a.win == nil {
		return
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "Export editable ER diagram", DefaultPath: strings.ToLower(f.table.Name) + "-relationships.drawio", Filters: []mygo.FileFilter{{Name: "draw.io diagram", Extensions: []string{"drawio"}}}})
	if err != nil {
		f.errorText = err.Error()
		return
	}
	if path == "" {
		return
	}
	document := tableERDDrawio(f.graph)
	go func() {
		err := os.WriteFile(path, []byte(document), 0600)
		a.win.Update(func() {
			if a.closed || a.tableERD != f {
				return
			}
			if err != nil {
				f.errorText = err.Error()
			} else {
				a.status = "ER diagram exported · " + path
			}
		})
	}()
}

func (a *App) tableERDDialog(c *ui.Context, k palette) {
	if !a.showTableERD || a.tableERD == nil {
		return
	}
	f := a.tableERD
	width, height := c.Size()
	ui.DialogBase(c.Key("table-erd-dialog"), &a.showTableERD, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(min(float32(1120), width-40)).Height(min(float32(760), height-40)).Radius(10).Clip().Background(k.panel).Border(1, k.border).Children(func() {
			ui.Row(c).FillWidth().Height(44).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "ERD · "+f.table.Label()).FontSize(15).FontWeight(600).Grow(1).TextColor(k.text)
				if ui.Button(c, "Export .drawio").Disabled(f.loading || len(f.graph.Tables) == 0).Clicked() {
					a.exportTableERD(f)
				}
				if ui.Button(c, "Close").Label("Close table ERD").Clicked() {
					a.showTableERD = false
				}
			})
			if f.loading {
				ui.Text(c, "Loading table relationships…").Margin(20).TextColor(k.muted)
			}
			if f.errorText != "" {
				ui.Text(c, f.errorText).Margin(12).TextColor(k.danger)
			}
			if !f.loading {
				nodes, graphWidth, graphHeight := layoutTableERD(f.graph)
				positions := map[string]erdNode{}
				for _, node := range nodes {
					positions[node.table.Table.ID()] = node
				}

				ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
					ui.ScrollHorizontal(c).Width(graphWidth).MinHeight(graphHeight).Children(func() {
						ui.Box(c).Width(graphWidth).Height(graphHeight).Background(k.editor).Draw(func(p *ui.Painter, r ui.Rect) {
							for _, relation := range f.graph.Relations {
								source, target := positions[relation.Source.ID()], positions[relation.Target.ID()]
								sx, tx := source.x+source.w, target.x
								if source.x > target.x {
									sx, tx = source.x, target.x+target.w
								}
								sy, ty := source.y+36, target.y+36
								line := func(x1, y1, x2, y2 float32) { p.Line(r.X+x1, r.Y+y1, r.X+x2, r.Y+y2, 1.5, k.muted) }
								mid := (sx + tx) / 2
								if source.x == target.x {
									line(sx, sy, sx+50, sy)
									line(sx+50, sy, sx+50, sy+40)
									line(sx+50, sy+40, sx, sy+40)
								} else {
									line(sx, sy, mid, sy)
									line(mid, sy, mid, ty)
									line(mid, ty, tx, ty)
									direction := float32(1)
									if tx < sx {
										direction = -1
									}
									line(tx-7*direction, ty-5, tx, ty)
									line(tx, ty, tx-7*direction, ty+5)
								}
							}
						}).Children(func() {
							for _, node := range nodes {
								ui.Column(c.Key(node.table.Table.ID())).Absolute().Left(node.x).Top(node.y).Width(node.w).Height(node.h).Padding(10).Gap(4).Radius(6).Background(k.panel).Border(1, k.border).Children(func() {
									ui.Text(c, node.table.Table.Label()).SingleLine().FontSize(13).FontWeight(600).TextColor(k.text)
									ui.Divider(c)
									for _, col := range node.table.Columns {
										ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
											icon := "type-unknown"
											if col.PrimaryKey {
												icon = "key"
											}
											ui.Icon(c, controlIcon(icon)).Size(12, 12).TextColor(k.muted)
											ui.Text(c, col.Name).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(k.text)
											ui.Text(c, col.Type).SingleLine().MaxWidth(110).FontSize(10).TextColor(k.muted)
										})
									}
								})
							}
						})
					})
				})
				ui.Text(c, "Arrows: foreign key → referenced table. One-hop relationships; up to 17 tables, 64 relationships and 50 columns per table.").Margin(8, 12).FontSize(10).TextColor(k.muted)
				if f.graph.Truncated {
					ui.Text(c, "The diagram is bounded; additional related objects are omitted.").Margin(0, 12, 8, 12).FontSize(11).TextColor(k.muted)
				}
			}
		})
	})
}
