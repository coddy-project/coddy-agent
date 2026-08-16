//go:build windows

package update

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func scheduleWindowsUpdate(req windowsUpdateRequest) error {
	if err := validateWindowsUpdateRequest(req); err != nil {
		return err
	}
	source, err := resolveExecutablePath()
	if err != nil {
		return err
	}
	helper, err := copyUpdateHelper(source)
	if err != nil {
		return fmt.Errorf("copy update helper: %w", err)
	}

	cmd := exec.Command(helper,
		applyUpdateCommand,
		"--parent-pid", strconv.Itoa(req.ParentPID),
		"--source", req.StagedPath,
		"--target", req.TargetPath,
		"--restart",
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(helper)
		return fmt.Errorf("start update helper: %w", err)
	}
	return nil
}

func runWindowsUpdateHelper(args []string, out io.Writer) error {
	fs := flag.NewFlagSet(applyUpdateCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	parentPID := fs.Int("parent-pid", 0, "parent process ID")
	source := fs.String("source", "", "staged executable")
	target := fs.String("target", "", "installed executable")
	restart := fs.Bool("restart", false, "restart Coddy after installing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := windowsUpdateRequest{
		ParentPID:  *parentPID,
		Restart:    *restart,
		StagedPath: strings.TrimSpace(*source),
		TargetPath: strings.TrimSpace(*target),
	}
	if err := validateWindowsUpdateRequest(req); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out, "Windows update helper started.")
	if err := waitForParentExit(req.ParentPID, out); err != nil {
		return err
	}
	helperPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve update helper path: %w", err)
	}
	return installWindowsUpdate(req, helperPath, out)
}

func runWindowsRestartAfterUpdate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet(restartAfterUpdateCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	helper := fs.String("helper", "", "temporary update helper")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := removeTemporaryHelper(strings.TrimSpace(*helper)); err != nil {
		_, _ = fmt.Fprintf(out, "Could not remove temporary update helper: %v\n", err)
	}
	target, err := resolveExecutablePath()
	if err != nil {
		return err
	}
	if err := restartCoddy(target); err != nil {
		return fmt.Errorf("restart Coddy after update: %w", err)
	}
	_, _ = fmt.Fprintln(out, "Coddy restarted.")
	return nil
}

func validateWindowsUpdateRequest(req windowsUpdateRequest) error {
	if req.ParentPID <= 0 {
		return fmt.Errorf("invalid update parent PID")
	}
	if req.StagedPath == "" || req.TargetPath == "" {
		return fmt.Errorf("update source and target are required")
	}
	if filepath.Clean(req.StagedPath) == filepath.Clean(req.TargetPath) {
		return fmt.Errorf("update source and target must differ")
	}
	return nil
}

func copyUpdateHelper(source string) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()

	helper, err := os.CreateTemp("", "coddy-update-helper-*.exe")
	if err != nil {
		return "", err
	}
	helperPath := helper.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(helperPath)
		}
	}()
	if _, err := io.Copy(helper, input); err != nil {
		_ = helper.Close()
		return "", err
	}
	if err := helper.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return helperPath, nil
}

func waitForParentExit(parentPID int, out io.Writer) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parentPID))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("open current Coddy process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	_, _ = fmt.Fprintln(out, "Waiting for the current Coddy process to exit...")
	state, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for current Coddy process: %w", err)
	}
	if state != uint32(windows.WAIT_OBJECT_0) {
		return fmt.Errorf("wait for current Coddy process returned %d", state)
	}
	_, _ = fmt.Fprintln(out, "Current Coddy process exited.")
	return nil
}

func installWindowsUpdate(req windowsUpdateRequest, helperPath string, out io.Writer) error {
	backup, err := copyWindowsBackup(req.TargetPath)
	if err != nil {
		return fmt.Errorf("back up current Coddy before update: %w", err)
	}
	keepBackup := true
	defer func() {
		if !keepBackup {
			_ = os.Remove(backup)
		}
	}()

	_, _ = fmt.Fprintln(out, "Installing downloaded update...")
	if err := renameWithRetry(req.StagedPath, req.TargetPath, out); err != nil {
		return fmt.Errorf("install update: %w; current Coddy was preserved at %s", err, req.TargetPath)
	}

	if req.Restart {
		_, _ = fmt.Fprintln(out, "Restarting Coddy...")
		if err := restartCoddy(req.TargetPath, restartAfterUpdateCommand, "--helper", helperPath); err != nil {
			if restoreErr := restoreWindowsBackup(req.TargetPath, backup); restoreErr != nil {
				return fmt.Errorf("restart updated Coddy: %w; restore previous Coddy: %v", err, restoreErr)
			}
			return fmt.Errorf("restart updated Coddy: %w; restored the previous version", err)
		}
	}
	keepBackup = false
	_, _ = fmt.Fprintln(out, "Coddy update installed successfully.")
	return nil
}

func copyWindowsBackup(target string) (string, error) {
	input, err := os.Open(target)
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()

	backup, err := os.CreateTemp(filepath.Dir(target), ".coddy-update-backup-*")
	if err != nil {
		return "", err
	}
	backupPath := backup.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(backupPath)
		}
	}()
	if _, err := io.Copy(backup, input); err != nil {
		_ = backup.Close()
		return "", err
	}
	if err := backup.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return backupPath, nil
}

func renameWithRetry(source, target string, out io.Writer) error {
	deadline := time.Now().Add(30 * time.Second)
	for attempt := 1; ; attempt++ {
		err := os.Rename(source, target)
		if err == nil {
			return nil
		}
		if !isRetryableWindowsLock(err) || time.Now().After(deadline) {
			return err
		}
		if attempt == 1 {
			_, _ = fmt.Fprintln(out, "Waiting for Windows to release the executable lock...")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isRetryableWindowsLock(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

func restartCoddy(target string, args ...string) error {
	cmd := exec.Command(target, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func removeTemporaryHelper(helper string) error {
	if !isTemporaryHelper(helper) {
		return fmt.Errorf("invalid temporary helper path %q", helper)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Remove(helper)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !isRetryableWindowsLock(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func isTemporaryHelper(path string) bool {
	if filepath.Base(path) == "." || !strings.HasPrefix(strings.ToLower(filepath.Base(path)), "coddy-update-helper-") || !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return false
	}
	rel, err := filepath.Rel(os.TempDir(), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func restoreWindowsBackup(target, backup string) error {
	failed := target + ".failed"
	if err := os.Rename(target, failed); err != nil {
		return fmt.Errorf("move failed update aside: %w", err)
	}
	if err := renameWithRetry(backup, target, io.Discard); err != nil {
		return err
	}
	return nil
}
