//go:build !windows

package app

import (
	"context"
	"errors"
)

const supportsGoogleCloudSetup = false

func findGoogleCloudCLI() string { return "" }

func openGoogleCloudInstaller() error {
	return errors.ErrUnsupported
}

func loginGoogleCloudADC(context.Context, string) error {
	return errors.ErrUnsupported
}
