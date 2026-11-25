package database

import (
	"os/exec"
	"syscall"
)

func hidePostgreSQLTool(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
