package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"dibiex/internal/database"
	"dibiex/internal/resultexport"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	exportLoadedRows = "Loaded rows"
	exportAllRows    = "All query rows"
)

type resultExportForm struct {
	window                                     *mygo.Window
	direct                                     bool
	started                                    time.Time
	client                                     *database.Client
	sourceContext                              context.Context
	rowScope, filter, filterScope              string
	sourceColumns                              []string
	columns                                    []int
	data                                       resultexport.Data
	options                                    resultexport.Options
	source, path, errorText, status, separator string
	replace, busy, advanced                    bool
	filenameStem, exportedPath                 string
	cancel                                     context.CancelFunc
	preview, previewError                      string
	previewOptions                             resultexport.Options
	previewReady, previewBusy                  bool
	previewVersion                             uint64
	appliedFormat, appliedExtension            string
}

func (a *App) openQueryExport() {
	tab := a.currentEditor()
	if a.client == nil || tab == nil || a.queryBusy() || a.resultExport != nil && a.resultExport.busy {
		return
	}
	// Capturing the editor selection must not change existing result diagnostics.
	line := a.executionLine
	query := strings.TrimSpace(a.querySQL())
	a.executionLine = line
	if strings.TrimSpace(query) == "" {
		return
	}
	a.runAction(query, false, "export")
}

func (a *App) openQueryExportWithSQL(query string) {
	tab := a.currentEditor()
	if a.client == nil || tab == nil || a.queryBusy() || a.resultExport != nil && a.resultExport.busy {
		return
	}
	a.showExportWindow(&resultExportForm{
		direct: true, client: a.client, sourceContext: a.connectionContext(), rowScope: exportAllRows,
		source: tab.name + " · " + a.workspaceName(), separator: "Comma", filenameStem: exportFilenameStem(tab.name),
		data:    resultexport.Data{SQL: query},
		options: resultexport.Options{Format: "CSV", Compression: "None", Header: true, Separator: ",", Pretty: true},
	})
}

func (a *App) openResultExport(snapshot *queryResultTab, rows, columns []int) {
	f := &resultExportForm{client: a.client, sourceContext: a.connectionContext(), sourceColumns: append([]string(nil), a.result.Columns...), columns: append([]int(nil), columns...), rowScope: exportLoadedRows, source: a.connection, separator: "Comma", options: resultexport.Options{Format: "CSV", Compression: "None", Header: true, Separator: ",", Pretty: true}}
	if tab := a.currentEditor(); tab != nil {
		f.filenameStem = exportFilenameStem(tab.name)
	}
	if snapshot != nil {
		f.source += fmt.Sprintf(" · Result %d", snapshot.id)
		f.data.SQL = snapshot.sql
		f.filter, f.filterScope = snapshot.view.filter, snapshot.view.scope
	}
	for _, col := range columns {
		f.data.Columns = append(f.data.Columns, a.result.Columns[col])
	}
	for _, index := range rows {
		row := make([]string, len(columns))
		for j, col := range columns {
			row[j] = a.result.Rows[index][col]
		}
		f.data.Rows = append(f.data.Rows, row)
	}
	if f.data.Rows == nil {
		f.data.Rows = [][]string{}
	}
	a.showExportWindow(f)
}

func streamResultExport(ctx context.Context, client *database.Client, source resultexport.Data, sourceColumns []string, columns []int, filter, scope string, write func(resultexport.Data) error, progress func(int64)) (int64, error) {
	if client == nil {
		return 0, errors.New("Reconnect the result's owning connection to export all rows.")
	}
	var count int64
	err := client.StreamExportQuery(ctx, source.SQL, func(actual []string, next func() ([]string, error)) error {
		if sourceColumns != nil && !slices.Equal(actual, sourceColumns) {
			return errors.New("Query columns changed since this result was loaded. Run the query again before exporting.")
		}
		d := source
		d.Rows = nil
		// Direct execution discovers columns in the same query it exports.
		if sourceColumns == nil {
			d.Columns = append([]string(nil), actual...)
			columns = make([]int, len(actual))
			for i := range columns {
				columns[i] = i
			}
		}
		needle := strings.ToLower(filter)
		column, scopeErr := strconv.Atoi(scope)
		lastProgress := time.Now()
		d.Next = func() ([]string, error) {
			for {
				row, err := next()
				if err != nil {
					return nil, err
				}
				match := needle == ""
				for j, cell := range row {
					if (scopeErr != nil || j == column) && strings.Contains(strings.ToLower(cell), needle) {
						match = true
						break
					}
				}
				if !match {
					continue
				}
				projected := make([]string, len(columns))
				for j, col := range columns {
					projected[j] = row[col]
				}
				count++
				if progress != nil && time.Since(lastProgress) >= 250*time.Millisecond {
					progress(count)
					lastProgress = time.Now()
				}
				return projected, nil
			}
		}
		return write(d)
	})
	return count, err
}

func exportCompressions(format string) []string {
	switch format {
	case "Parquet":
		return []string{"Snappy", "Zstandard", "GZIP", "None"}
	case "Excel (xlsx)":
		return []string{"ZIP", "None"}
	default:
		return []string{"None", "GZIP"}
	}
}

func (f *resultExportForm) changeExtension(old string) {
	if f.path != "" && strings.HasSuffix(strings.ToLower(f.path), "."+old) {
		f.path = f.path[:len(f.path)-len(old)] + f.options.Extension()
	}
}

func (f *resultExportForm) syncOptions() {
	if f.appliedFormat != "" && f.appliedFormat != f.options.Format {
		f.options.Compression = exportCompressions(f.options.Format)[0]
	}
	if f.appliedExtension != "" && f.appliedExtension != f.options.Extension() {
		f.changeExtension(f.appliedExtension)
	}
	f.appliedFormat, f.appliedExtension = f.options.Format, f.options.Extension()
	if f.options.Format == "CSV" {
		switch f.separator {
		case "Comma":
			f.options.Separator = ","
		case "Semicolon":
			f.options.Separator = ";"
		case "Tab":
			f.options.Separator = "\t"
		case "Pipe":
			f.options.Separator = "|"
		}
	}
}

type exportPreviewWriter struct{ bytes.Buffer }

func (w *exportPreviewWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *exportPreviewWriter) Write(p []byte) (int, error) {
	if len(p) > 65536-w.Len() {
		n, _ := w.Buffer.Write(p[:65536-w.Len()])
		return n, io.ErrShortWrite
	}
	return w.Buffer.Write(p)
}

func resultExportPreview(d resultexport.Data, options resultexport.Options) (string, string) {
	d.Rows = d.Rows[:min(10, len(d.Rows))]
	options.Compression = "None"
	var out exportPreviewWriter
	err := resultexport.Write(context.Background(), &out, d, options)
	if errors.Is(err, io.ErrShortWrite) {
		return out.String() + "\n… preview shortened", ""
	}
	if err != nil {
		return "", err.Error()
	}
	return out.String(), ""
}

func (a *App) updateExportPreview(f *resultExportForm) {
	if f.direct {
		sql := f.data.SQL
		if len(sql) > 65536 {
			sql = strings.ToValidUTF8(sql[:65536], "") + "\n… SQL preview shortened; the full query will execute."
		}
		f.preview, f.previewError, f.previewBusy = sql, "", false
		return
	}
	if f.previewReady && f.previewOptions == f.options {
		return
	}
	f.previewOptions, f.previewReady = f.options, true
	f.previewVersion++
	version := f.previewVersion
	options := f.options
	if options.Format == "Excel (xlsx)" || options.Format == "Parquet" {
		f.preview, f.previewError, f.previewBusy = "Binary format · text preview is not available.", "", false
		return
	}
	if f.window == nil {
		// Offscreen native UI tests have no event-loop window.
		f.preview, f.previewError = resultExportPreview(f.data, options)
		f.previewBusy = false
		return
	}
	f.previewBusy = true
	d := f.data
	win := f.window
	go func() {
		preview, message := resultExportPreview(d, options)
		win.Update(func() {
			if a.closed || a.resultExport != f || f.previewVersion != version {
				return
			}
			f.preview, f.previewError, f.previewBusy = preview, message, false
		})
	}()
}

func exportFilenameStem(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".sql") {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		return "result"
	}
	return name + "-export"
}

func (f *resultExportForm) suggestedFilename() string {
	stem := f.filenameStem
	if stem == "" {
		stem = "result"
	}
	return stem + "." + f.options.Extension()
}

func (a *App) pickExportFile(f *resultExportForm) {
	if f.window == nil {
		return
	}
	ext := f.options.Extension()
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: f.window, Title: f.title(), DefaultPath: f.suggestedFilename(), Filters: []mygo.FileFilter{{Name: f.options.Format, Extensions: []string{ext}}, {Name: "All files", Extensions: []string{"*"}}}})
	if err != nil {
		f.errorText = err.Error()
	} else if path != "" {
		f.path = path
	}
}

func (a *App) startResultExport(f *resultExportForm, clipboard bool) {
	if f.busy || f.window == nil {
		return
	}
	validateErr := f.options.ValidateOptions()
	if !f.direct {
		validateErr = f.options.Validate(f.data)
	}
	if validateErr != nil {
		f.errorText = validateErr.Error()
		return
	}
	if !clipboard && strings.TrimSpace(f.path) == "" {
		a.pickExportFile(f)
		if strings.TrimSpace(f.path) == "" {
			return
		}
	}
	allRows := f.rowScope == exportAllRows
	if clipboard && allRows {
		f.errorText = "Choose Export to File for all query rows, or select Loaded rows to copy to the clipboard."
		return
	}
	parent := f.sourceContext
	if parent == nil {
		parent = a.connectionContext()
	}
	ctx, cancel := context.WithCancel(parent)
	f.busy, f.cancel, f.errorText, f.status = true, cancel, "", "Writing file…"
	f.exportedPath = ""
	if clipboard {
		f.status = "Preparing clipboard…"
	}
	f.started = time.Now()
	if allRows {
		f.status = "Running query…"
	}
	d, options, path, replace, win := f.data, f.options, f.path, f.replace, f.window
	client, sourceColumns, columns, filter, scope := f.client, f.sourceColumns, f.columns, f.filter, f.filterScope
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				win.Update(func() {})
			}
		}
	}()
	go func() {
		defer close(done)
		defer cancel()
		var out bytes.Buffer
		var err error
		if !clipboard {
			path, err = filepath.Abs(path)
		}
		count := int64(len(d.Rows))
		if err != nil {
			win.Update(func() {
				f.busy, f.cancel, f.errorText, f.status = false, nil, err.Error(), ""
			})
			return
		}
		if clipboard {
			options.Compression = "None"
			err = resultexport.Write(ctx, &out, d, options)
		} else if allRows {
			err = resultexport.SaveGenerated(ctx, path, replace, func(target io.Writer) error {
				var streamErr error
				count, streamErr = streamResultExport(ctx, client, d, sourceColumns, columns, filter, scope, func(stream resultexport.Data) error {
					win.Update(func() {
						if !a.closed && a.resultExport == f && f.busy {
							f.status = "Writing file…"
						}
					})
					return resultexport.Write(ctx, target, stream, options)
				}, func(n int64) {
					win.Update(func() {
						if !a.closed && a.resultExport == f && f.busy {
							f.status = fmt.Sprintf("Writing file… %d rows", n)
						}
					})
				})
				return streamErr
			})
		} else {
			err = resultexport.Save(ctx, path, replace, d, options)
		}
		win.Update(func() {
			if a.closed || a.resultExport != f {
				return
			}
			f.busy, f.cancel = false, nil
			if err != nil {
				f.errorText, f.status = err.Error(), ""
				if errors.Is(err, context.Canceled) {
					f.errorText, f.status = "", "Export canceled."
				}
				return
			}
			if clipboard {
				mygo.Clipboard.WriteText(out.String())
				f.status = fmt.Sprintf("Copied %d rows to clipboard.", len(d.Rows))
			} else {
				f.exportedPath = path
				f.status = fmt.Sprintf("Exported %d rows to %s", count, filepath.Base(path))
			}
		})
	}()
}

func (f *resultExportForm) title() string {
	if f.direct {
		return "Execute to File"
	}
	return "Export Data"
}

func (a *App) showExportWindow(f *resultExportForm) {
	if a.showResultExport {
		if current := a.resultExport; current != nil && current.window != nil {
			current.window.Focus()
		}
		return
	}
	a.resultExport, a.showResultExport = f, true
	if a.win == nil {
		return
	}
	f.window = newModalWindow(mygo.WindowOptions{
		Title: f.title(), Width: 960, Height: 724, MinWidth: 520, MinHeight: 440,
		Parent: a.win,
	}, func(c *ui.Context) {
		width, _ := c.Size()
		k := paletteOf(c)
		ui.Column(c).Fill().Background(k.panel).Label(f.title() + " dialog").Children(func() {
			a.resultExportView(c, k, f, width)
		})
	})
	f.window.OnClose(func(e *mygo.CloseEvent) {
		if f.busy {
			e.PreventDefault()
		}
	})
	f.window.OnClosed(func() {
		f.window = nil
		if a.resultExport == f {
			a.resultExport, a.showResultExport = nil, false
			if !a.closed {
				a.win.Update(func() {})
			}
		}
	})
}

func (a *App) closeExportWindow(f *resultExportForm) {
	if f.window != nil {
		f.window.Close()
		return
	}
	a.resultExport, a.showResultExport = nil, false
}

func (a *App) resultExportDialog(c *ui.Context, k palette) {
	f := a.resultExport
	if a.win != nil || !a.showResultExport || f == nil {
		return
	}
	width, height := c.Size()
	open := true
	ui.DialogBase(c.Key("result-export-dialog"), &open, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, .4))
		panel.Width(min(float32(960), width-32)).Height(min(float32(680), height-40)).Padding(0).Radius(10).Clip().Background(k.panel).Border(1, k.border).Label(f.title() + " dialog").Children(func() {
			ui.Text(c, f.title()).Padding(12, 16).FontSize(16).FontWeight(600).TextColor(k.text)
			a.resultExportView(c, k, f, width)
		})
	})
	if !open && !f.busy {
		a.closeExportWindow(f)
	}
}

func (a *App) resultExportView(c *ui.Context, k palette, f *resultExportForm, width float32) {
	f.syncOptions()
	a.updateExportPreview(f)
	ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		body := ui.Column(c).FillWidth().Padding(18).Gap(20)
		if width >= 900 {
			body = ui.Row(c).FillWidth().Padding(18).Gap(24).AlignItems(ui.Start)
		}
		body.Children(func() {
			left := ui.Column(c).Gap(14).MinWidth(0).Disabled(f.busy)
			if width >= 900 {
				left.Width(420)
			} else {
				left.FillWidth()
			}
			left.Children(func() {
				designerField(c, k, "Source", func() {
					ui.Text(c, f.source).SingleLine().FillWidth().Padding(8).Border(1, k.border).FontSize(12).TextColor(k.text)
				})
				if f.direct {
					ui.Text(c, "Executes this SQL once and streams all rows directly to the chosen file. SQL LIMIT and billing caps still apply. Uses a separate read-only session.").FontSize(11).TextColor(k.muted)
				} else {
					summary := fmt.Sprintf("%d loaded rows · %d visible columns", len(f.data.Rows), len(f.data.Columns))
					if f.filter != "" {
						summary += " · Filter: " + f.filter
					}
					ui.Text(c, summary).MaxHeight(42).FontSize(12).TextColor(k.text).Tooltip(summary)
					designerField(c, k, "Rows", func() {
						ui.Column(c).FillWidth().Gap(6).Label("Export row scope").Children(func() {
							ui.Radio(c.Key("export-loaded"), &f.rowScope, exportLoadedRows, fmt.Sprintf("Loaded rows (%d)", len(f.data.Rows)))
							ui.Radio(c.Key("export-all"), &f.rowScope, exportAllRows, "All matching query rows").Disabled(f.data.SQL == "" || f.client == nil)
						})
					})
				}
				if !f.direct && f.rowScope == exportAllRows {
					ui.Text(c, "Reruns this query in a separate read-only session and streams all matching rows. SQL LIMIT and billing caps still apply. Uses query ordering; editor-only settings and temporary objects are not shared.").FontSize(11).TextColor(k.muted)
				}
				designerField(c, k, "Format", func() {
					ui.Select(c.Key("export-format"), &f.options.Format, []string{"CSV", "Excel (xlsx)", "JSON", "Parquet"}).FillWidth().Label("Export format")
				})
				ui.Checkbox(c, &f.advanced, "Advanced options")
				if f.advanced {
					designerField(c, k, "Compression", func() {
						ui.Select(c.Key("export-compression"), &f.options.Compression, exportCompressions(f.options.Format)).FillWidth().Label("Export compression")
					})
					switch f.options.Format {
					case "CSV":
						ui.Checkbox(c, &f.options.Header, "Include column headers").Label("Include CSV header")
						designerField(c, k, "Separator", func() {
							ui.Select(c.Key("export-separator"), &f.separator, []string{"Comma", "Semicolon", "Tab", "Pipe", "Custom"}).FillWidth().Label("CSV separator")
							switch f.separator {
							case "Comma":
								f.options.Separator = ","
							case "Semicolon":
								f.options.Separator = ";"
							case "Tab":
								f.options.Separator = "\t"
							case "Pipe":
								f.options.Separator = "|"
							case "Custom":
								ui.TextInput(c.Key("export-custom-separator"), &f.options.Separator).FillWidth().Label("Custom CSV separator")
							}
						})
						ui.Checkbox(c, &f.options.CRLF, "Windows line endings (CRLF)")
						ui.Checkbox(c, &f.options.BOM, "UTF-8 BOM (Excel compatibility)")
					case "Excel (xlsx)":
						ui.Checkbox(c, &f.options.QuerySheet, "Output query to separate sheet")
					case "JSON":
						ui.Checkbox(c, &f.options.Pretty, "Pretty print")
						ui.Checkbox(c, &f.options.JSONRows, "Rows with column names (preserves duplicate names)")
					}
					ui.Checkbox(c, &f.replace, "Replace existing file")
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
				if f.direct {
					ui.Text(c, "Query to execute").FontSize(13).FontWeight(600).TextColor(k.text)
					ui.Text(c, "Runs when you choose Execute to File").FontSize(11).TextColor(k.muted)
				} else {
					heading := "Export preview"
					if f.rowScope == exportAllRows {
						heading = "Sample from loaded results"
					}
					ui.Text(c, heading).FontSize(13).FontWeight(600).TextColor(k.text)
					ui.Text(c, fmt.Sprintf("%d of %d loaded rows · before compression", min(10, len(f.data.Rows)), len(f.data.Rows))).FontSize(11).TextColor(k.muted)
				}
				ui.Scroll(c).FillWidth().Height(280).Background(k.editor).Border(1, k.border).Children(func() {
					text := f.preview
					if f.previewBusy {
						text = "Preparing preview…"
					}
					if f.previewError != "" {
						text = f.previewError
					}
					ui.Text(c, text).Padding(12).Font("JetBrains Mono").FontSize(12).TextColor(k.text)
				})
				note := "All-row exports read full text values; cells over 16 MiB or rows over 32 MiB fail explicitly. Loaded-row exports use displayed values. NULL/BLOB labels are preserved."
				if f.direct {
					note = "Exports full text values with NULL/BLOB labels. Cells over 16 MiB or rows over 32 MiB fail explicitly."
				}
				ui.Text(c, note).FontSize(11).TextColor(k.muted)
			})
		})
	})
	ui.Column(c).FillWidth().Padding(12, 16).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(k.border).Children(func() {
		if f.errorText != "" {
			ui.Text(c, f.errorText).MaxHeight(60).FontSize(11).TextColor(k.danger)
		}
		if f.status != "" {
			status := f.status
			if f.busy && !f.started.IsZero() {
				status += " · " + time.Since(f.started).Round(time.Second).String()
			}
			ui.Text(c, status).MaxHeight(40).FontSize(11).TextColor(k.muted)
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextInput(c.Key("export-path"), &f.path).Grow(1).MinWidth(0).Disabled(f.busy).Placeholder(f.suggestedFilename()).Label("Export output file")
			if toolbarAction(c, k, "folder-open", "Choose export file", k.muted).Disabled(f.busy).Clicked() {
				a.pickExportFile(f)
			}
			buttonTitle, buttonLabel := "Export to File", "Export to File"
			if f.direct {
				buttonTitle, buttonLabel = "Execute to File", "Start file export"
			}
			if ui.PrimaryButton(c, buttonTitle).Label(buttonLabel).Disabled(f.busy).Clicked() {
				a.startResultExport(f, false)
			}
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if f.exportedPath != "" {
				if ui.Button(c, "Open folder").Clicked() {
					if err := mygo.Shell.OpenPath(filepath.Dir(f.exportedPath)); err != nil {
						f.errorText = "Could not open the export folder: " + err.Error()
					}
				}
			}
			ui.Spacer(c)
			label := "Close"
			if f.busy {
				label = "Cancel export"
			}
			if ui.Button(c, label).Label("Cancel result export").Clicked() {
				if f.busy {
					if f.cancel != nil {
						f.cancel()
					}
				} else {
					a.closeExportWindow(f)
				}
			}
			if !f.direct && ui.Button(c, "Copy to Clipboard").Disabled(f.busy || f.rowScope == exportAllRows || f.options.Format == "Excel (xlsx)" || f.options.Format == "Parquet").Tooltip("Clipboard copying supports Loaded rows in text formats. Export to File streams all query rows.").Clicked() {
				a.startResultExport(f, true)
			}
		})
	})
}
