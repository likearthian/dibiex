package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const supportsGoogleCloudSetup = true
const googleCloudInstallerURL = "https://dl.google.com/dl/cloudsdk/channels/rapid/GoogleCloudSDKInstaller.exe"

var googleCloudSignIn sync.Mutex

func findGoogleCloudCLI() string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	// Read the current registry PATH as installers cannot update this process's environment.
	for _, location := range []struct {
		root registry.Key
		key  string
	}{
		{registry.CURRENT_USER, "Environment"},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	} {
		key, err := registry.OpenKey(location.root, location.key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, kind, err := key.GetStringValue("Path")
		key.Close()
		if err == nil && kind == registry.EXPAND_SZ {
			path, err = registry.ExpandString(path)
		}
		if err == nil {
			dirs = append(dirs, filepath.SplitList(path)...)
		}
	}
	for _, name := range []string{"LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(name); filepath.IsAbs(base) {
			dirs = append(dirs, filepath.Join(base, "Google", "Cloud SDK", "google-cloud-sdk", "bin"))
		}
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, "gcloud.cmd")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func openGoogleCloudInstaller() error {
	if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(googleCloudInstallerURL), nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("open the Google Cloud CLI download: %w", err)
	}
	return nil
}

func loginGoogleCloudADC(ctx context.Context, path string) error {
	if !googleCloudSignIn.TryLock() {
		return fmt.Errorf("Google sign-in is already running")
	}
	defer googleCloudSignIn.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// cmd.exe expands percent signs even inside quotes. Run only a fixed
	// command with an absolute, quoted launcher path and no form inputs.
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\"%\r\n\x00") {
		return fmt.Errorf("the Google Cloud CLI path cannot be used for guided sign-in")
	}
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("locate Windows command processor: %w", err)
	}
	cmd := filepath.Join(systemDir, "cmd.exe")
	commandLine := `"` + cmd + `" /D /V:OFF /S /C ""` + path + `" auth application-default login"`

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("prepare Google sign-in: %w", err)
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("prepare cancellable Google sign-in: %w", err)
	}

	// Start suspended so the CLI and its child processes can be canceled together.
	var startup windows.StartupInfo
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	if err := windows.CreateProcess(windows.StringToUTF16Ptr(cmd), windows.StringToUTF16Ptr(commandLine), nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_NEW_CONSOLE, nil, windows.StringToUTF16Ptr(systemDir), &startup, &process); err != nil {
		return fmt.Errorf("start Google Cloud CLI: %w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return fmt.Errorf("prepare cancellable Google sign-in: %w", err)
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return fmt.Errorf("start Google sign-in: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := windows.WaitForSingleObject(process.Process, 100)
		if err != nil {
			return fmt.Errorf("wait for Google sign-in: %w", err)
		}
		if result == windows.WAIT_OBJECT_0 {
			break
		}
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return fmt.Errorf("read Google sign-in result: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("Google Cloud CLI sign-in failed (exit code %d). Try again or run gcloud auth application-default login in a terminal", exitCode)
	}
	return nil
}
