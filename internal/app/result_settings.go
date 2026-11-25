package app

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type resultPreferences struct {
	Density       string
	Font          string
	HideGridLines bool
	MarkEmpty     bool
	ShowControls  bool
}

type resultSettings struct {
	current resultPreferences
	window  *mygo.Window
	loading bool
	saving  bool
	error   string
}

func (p resultPreferences) appearance() (font string, size, height float32) {
	font, size, height = "JetBrains Mono", 12, 22
	if p.Font == "Inter" {
		font = "Inter"
	}
	switch p.Density {
	case "Compact":
		size, height = 11, 18
	case "Comfortable":
		size, height = 14, 28
	}
	return
}

var resultControlCharacters = strings.NewReplacer("\\", "\\\\", "\r", "\\r", "\n", "\\n", "\t", "\\t")

func (p resultPreferences) format(value string) string {
	if value == "" && p.MarkEmpty {
		return "⟨empty⟩"
	}
	if p.ShowControls {
		return resultControlCharacters.Replace(value)
	}
	return value
}

func resultSettingsPath() (string, error) {
	path, err := connectionPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "result-settings.json"), nil
}

func (w *connectionWorkspace) loadResultSettings(main *mygo.Window) {
	s := &w.resultSettings
	s.loading = true
	go func() {
		var prefs resultPreferences
		path, err := resultSettingsPath()
		if err == nil {
			var data []byte
			data, err = os.ReadFile(path)
			if err == nil {
				err = json.Unmarshal(data, &prefs)
			}
		}
		main.Update(func() {
			s.loading = false
			if err == nil {
				s.current = prefs
			} else if !errors.Is(err, os.ErrNotExist) {
				log.Printf("Load result settings: %v", err)
			}
			if s.window != nil {
				s.window.Update(func() {})
			}
		})
	}()
}

func writeResultSettings(prefs resultPreferences) error {
	path, err := resultSettingsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "result-settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(prefs)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (a *App) openResultSettings() {
	s := &a.ensureWorkspace().resultSettings
	if s.window != nil {
		s.window.Show()
		s.window.Focus()
		return
	}
	draft := s.current
	initialized := !s.loading && !s.saving
	s.error = ""
	s.window = newModalWindow(mygo.WindowOptions{
		Title: "Settings", Width: 480, Height: 534, MinWidth: 380, MinHeight: 434,
		Parent: a.win,
	}, func(c *ui.Context) {
		if !initialized && !s.loading && !s.saving {
			draft, initialized = s.current, true
		}
		if draft.Density == "" {
			draft.Density = "Standard"
		}
		if draft.Font == "" {
			draft.Font = "JetBrains Mono"
		}
		if s.loading {
			ui.Text(c, "Loading settings…").Padding(20)
			return
		}
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).FillWidth().Padding(20).Gap(10).Children(func() {
				ui.Text(c, "Result table").FontSize(20)
				ui.Text(c, "Appearance").FontSize(14)
				ui.Text(c, "Row density").FontSize(12)
				ui.Select(c.Key("density"), &draft.Density, []string{"Compact", "Standard", "Comfortable"}).Label("Row density").FillWidth()
				ui.Text(c, "Cell font").FontSize(12)
				ui.Select(c.Key("font"), &draft.Font, []string{"JetBrains Mono", "Inter"}).Label("Cell font").FillWidth()
				ui.Checkbox(c, &draft.HideGridLines, "Hide grid lines")
				ui.Divider(c)
				ui.Text(c, "Data formatting").FontSize(14)
				ui.Checkbox(c, &draft.MarkEmpty, "Mark empty strings as ⟨empty⟩")
				ui.Checkbox(c, &draft.ShowControls, "Show tabs, line breaks, and backslashes as escapes")
				ui.Text(c, "Formatting applies to the display. Copy and export use the original values.").FontSize(12)
				if s.error != "" {
					ui.Text(c, s.error).FontSize(12)
				}
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "Reset defaults").Disabled(s.saving).Clicked() {
						draft = resultPreferences{}
					}
					ui.Spacer(c)
					label := "Apply"
					if s.saving {
						label = "Saving…"
					}
					if ui.Button(c, label).Disabled(s.saving).Clicked() {
						a.saveResultSettings(s, draft)
					}
				})
			})
		})
	})
	s.window.OnClose(func(e *mygo.CloseEvent) {
		if s.saving {
			e.PreventDefault()
		}
	})
	s.window.OnClosed(func() { s.window = nil })
}

func (a *App) saveResultSettings(s *resultSettings, prefs resultPreferences) {
	s.saving, s.error = true, ""
	go func() {
		err := writeResultSettings(prefs)
		a.win.Update(func() {
			s.saving = false
			if err == nil {
				s.current = prefs
			} else {
				s.error = "Could not save result table settings."
				log.Printf("Save result settings: %v", err)
			}
			if s.window != nil {
				s.window.Update(func() {})
			}
		})
	}()
}
