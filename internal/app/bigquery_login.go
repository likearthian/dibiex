package app

import (
	"context"
	"errors"
	"os"
	"time"

	"dbcraft/internal/database"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) pickGoogleJSON(target *string, title string) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.connectionDialogParent(), Title: title, Filters: []mygo.FileFilter{{Name: "JSON credentials", Extensions: []string{"json"}}}})
	if err != nil {
		a.connectionForm.errorText = err.Error()
		return
	}
	if len(paths) > 0 {
		*target = paths[0]
		a.connectionForm.infoText = ""
	}
}

func (a *App) bigQueryAuthentication(c *ui.Context, k palette) {
	f := &a.connectionForm
	f.config.BigQueryAuth = f.config.BigQueryAuthMethod()
	ui.Text(c, "Authentication").FontSize(11).TextColor(k.muted)
	ui.Select(c.Key("bigquery-auth"), &f.config.BigQueryAuth, []string{database.BigQueryADC, database.BigQueryServiceAccount}).FillWidth().Disabled(a.busy).Label("Authentication")
	switch f.config.BigQueryAuthMethod() {
	case database.BigQueryADC:
		ui.Text(c, "Uses existing Application Default Credentials. Test or connect to check them.").FontSize(11).TextColor(k.muted)
		if supportsGoogleCloudSetup {
			a.bigQueryADCSetup(c, k)
		}
	case database.BigQueryServiceAccount:
		connectionField(c, k, "Service account JSON file", &f.config.CredentialFile, false)
		if ui.Button(c, "Browse service account…").Disabled(a.busy).Clicked() {
			a.pickGoogleJSON(&f.config.CredentialFile, "Choose service account key")
		}
		ui.Text(c, "Only the file path is saved. Keep the key file available on this computer.").FontSize(11).TextColor(k.muted)
	}
}

func (a *App) bigQueryADCSetup(c *ui.Context, k palette) {
	f := &a.connectionForm
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" {
		ui.Text(c, "GOOGLE_APPLICATION_CREDENTIALS is set. Google uses that credential file before local Google sign-in.").FontSize(11).TextColor(k.muted)
		return
	}
	if os.Getenv("CLOUDSDK_CONFIG") != "" {
		ui.Text(c, "A custom CLOUDSDK_CONFIG is set. For guided sign-in, start dibiex with the default Google Cloud CLI configuration.").FontSize(11).TextColor(k.muted)
		return
	}
	if f.adcSigningIn {
		if ui.Button(c, "Cancel Google sign-in").Clicked() && a.cancel != nil {
			a.cancel()
		}
		return
	}
	if f.gcloudPath != "" {
		ui.Text(c, "Google sign-in can replace local credentials shared by apps on this Windows account.").FontSize(11).TextColor(k.muted)
	} else if f.adcChecked {
		ui.Text(c, "Google Cloud CLI was not found. Run the installer, then check again.").FontSize(11).TextColor(k.muted)
	}
	ui.Row(c).Gap(8).Children(func() {
		label := "Check for Google Cloud CLI"
		if f.adcChecked {
			label = "Check again"
		}
		if ui.Button(c, label).Disabled(a.busy).Clicked() {
			a.runBigQueryADCSetup("Checking for Google Cloud CLI…", false, func(context.Context) (string, error) {
				return findGoogleCloudCLI(), nil
			})
		}
		if f.adcChecked && f.gcloudPath == "" {
			if ui.Button(c, "Download installer").Disabled(a.busy).Clicked() {
				a.runBigQueryADCSetup("Opening Google’s installer download…", false, func(context.Context) (string, error) {
					return "", openGoogleCloudInstaller()
				})
			}
		}
		if f.gcloudPath != "" && ui.Button(c, "Sign in with Google and connect").Disabled(a.busy || a.connectionStoreBusy).Clicked() {
			path := f.gcloudPath
			if _, err := a.formConfig(); err != nil {
				f.errorText, f.infoText = err.Error(), ""
				return
			}
			a.runBigQueryADCSetup("Complete Google sign-in in your browser…", true, func(ctx context.Context) (string, error) {
				return path, loginGoogleCloudADC(ctx, path)
			})
		}
	})
}

func (a *App) runBigQueryADCSetup(status string, signIn bool, work func(context.Context) (string, error)) {
	if a.busy || a.closed || a.win == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	a.busy, a.cancel, a.started = true, cancel, time.Time{}
	f := &a.connectionForm
	f.adcSigningIn, f.errorText, f.infoText = signIn, "", status
	win := a.win
	go func() {
		defer cancel()
		path, err := work(ctx)
		win.Update(func() {
			if a.closed {
				return
			}
			a.busy, a.cancel = false, nil
			f.adcSigningIn, f.infoText = false, ""
			if !a.showConnection {
				return
			}
			if errors.Is(err, context.Canceled) {
				f.infoText = "Google sign-in canceled."
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				f.errorText = "Google sign-in timed out. Try again."
				return
			}
			if err != nil {
				f.errorText = err.Error()
				return
			}
			f.adcChecked, f.gcloudPath = true, path
			if signIn {
				cfg, err := a.formConfig()
				if err != nil {
					f.errorText = err.Error()
					return
				}
				if a.connectionStoreBusy {
					f.infoText = "Google sign-in complete. Click Connect when the current connection operation finishes."
					return
				}
				a.connect(cfg)
			}
		})
	}()
}
