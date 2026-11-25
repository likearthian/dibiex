package app

import "github.com/egoist/mygo/ui"

const paneGripSize float32 = 9

// paneSplit follows the sibling app's centered grip while keeping the full
// gutter available for dragging and keyboard resizing.
func paneSplit(c *ui.Context, k palette, label string, size *float32, gutter float32, vertical bool, first, second func()) ui.Element {
	var box ui.Element
	if vertical {
		box = ui.Column(c)
	} else {
		box = ui.Row(c)
	}
	box.MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	total := box.Bounds().H - gutter
	if !vertical {
		total = box.Bounds().W - gutter
	}
	box.Children(func() {
		pane := ui.Box(c).MinWidth(0).MinHeight(0).Clip()
		if vertical {
			share := min(max(*size/100, .12), .88)
			pane.Grow(share).Basis(0).Children(first)
		} else {
			pane.Width(*size).Shrink(0).Children(first)
		}
		grip := ui.Box(c.Key("grip")).Shrink(0).Focusable().Role(ui.RoleSplitter).Label(label)
		back, forward := ui.KeyLeft, ui.KeyRight
		if vertical {
			grip.Height(gutter).Cursor(ui.CursorResizeRow)
			back, forward = ui.KeyUp, ui.KeyDown
		} else {
			grip.Width(gutter).Cursor(ui.CursorResizeColumn)
		}
		dx, dy, held := grip.Dragged()
		delta := dx
		if vertical {
			delta = dy
		}
		if grip.Shortcut(0, back) {
			delta -= 8
		}
		if grip.Shortcut(0, forward) {
			delta += 8
		}
		if delta != 0 && total > 0 {
			if vertical {
				*size = min(max(*size+100*delta/total, 12), 88)
			} else {
				*size = min(max(*size+delta, 0), total)
			}
			c.Invalidate()
		}
		if held || grip.Hovered() || grip.Pressed() || grip.FocusVisible() {
			grip.Draw(func(p *ui.Painter, r ui.Rect) {
				w, h := min(r.W, 3), min(r.H, 36)
				if vertical {
					w, h = min(r.W, 36), min(r.H, 3)
				}
				p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}, k.muted.Alpha(.6), 1.5)
			})
		}
		rest := ui.Box(c).Grow(1).MinWidth(0).MinHeight(0).Clip()
		if vertical {
			rest.Grow(1 - min(max(*size/100, .12), .88)).Basis(0)
		}
		rest.Children(second)
	})
	return box
}
