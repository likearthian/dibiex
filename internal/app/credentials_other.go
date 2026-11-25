//go:build !windows

package app

import "fmt"

func protectCredential([]byte) ([]byte, error) {
	return nil, fmt.Errorf("permanent password storage is currently supported on Windows; choose Until app restarts")
}
func unprotectCredential([]byte) ([]byte, error) {
	return nil, fmt.Errorf("Windows account credentials are unavailable on this platform")
}
