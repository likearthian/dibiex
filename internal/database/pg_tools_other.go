//go:build !windows

package database

import "os/exec"

func hidePostgreSQLTool(*exec.Cmd) {}
