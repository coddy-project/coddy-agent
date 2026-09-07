//go:build windows

package update

import (
	"context"
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

// helperCommandsAvailable enables the private update commands; only a Windows
// build has a helper that speaks them.
var helperCommandsAvailable = true

// renameFile, restartProbe, and startProcess are the seams the update tests
// replace. Production always renames through the operating system and starts
// the real executable.
var (
	renameFile   = os.Rename
	restartProbe = targetUnderstandsRestartHandoff
	startProcess = restartCoddy
)

// helperPrefix names the copies of Coddy that install an update from the system
// temporary directory. Both creating and recognising a helper go through it, so
// a stale one is always identifiable as ours.
const helperPrefix = "coddy-update-helper-"

var (
	// sharingViolationDeadline covers another process holding the executable
	// open - an antivirus scanner, an indexer, a shell preview handler. Those
	// let go on their own, so waiting is worth it.
	sharingViolationDeadline = 30 * time.Second
	// accessDeniedDeadline is deliberately short. Windows reports a still
	// mapped image and a plain permission failure with the same code, and the
	// second one never resolves by waiting.
	accessDeniedDeadline = 5 * time.Second
)

func scheduleWindowsUpdate(req windowsUpdateRequest) error {
	if err := validateWindowsUpdateRequest(req); err != nil {
		return err
	}
	// A helper whose restart step could not delete it outlives its update.
	// Sweeping here bounds the leak to one file instead of one per run.
	sweepOrphanedHelpers()

	source, err := resolveExecutablePath()
	if err != nil {
		return err
	}
	started, err := processCreationTime(windows.CurrentProcess())
	if err != nil {
		return fmt.Errorf("read current Coddy start time: %w", err)
	}
	helper, err := copyUpdateHelper(source)
	if err != nil {
		return fmt.Errorf("copy update helper: %w", err)
	}

	cmd := exec.Command(helper, helperArgs(req, started)...)
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

// helperArgs is the command line the helper parses back into the request. The
// request is the only thing that decides whether Coddy starts again, so the
// restart flag has to be conditional here rather than always present.
func helperArgs(req windowsUpdateRequest, parentStarted int64) []string {
	args := []string{
		applyUpdateCommand,
		"--parent-pid", strconv.Itoa(req.ParentPID),
		"--parent-started", strconv.FormatInt(parentStarted, 10),
		"--source", req.StagedPath,
		"--target", req.TargetPath,
	}
	if req.Restart {
		args = append(args, "--restart")
	}
	return args
}

func runWindowsUpdateHelper(args []string, out io.Writer) error {
	fs := flag.NewFlagSet(applyUpdateCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	parentPID := fs.Int("parent-pid", 0, "parent process ID")
	parentStarted := fs.Int64("parent-started", 0, "parent process creation time in nanoseconds")
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
	if err := waitForParentExit(req.ParentPID, *parentStarted, out); err != nil {
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
	probe := fs.Bool("probe", false, "exit successfully to report that this build understands the handoff")
	noRestart := fs.Bool("no-restart", false, "delete the temporary helper without starting Coddy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The helper asks before it hands over, so the probe must stay side effect
	// free: an older build answers the same question by failing to parse it.
	if *probe {
		return nil
	}
	if err := removeTemporaryHelper(strings.TrimSpace(*helper)); err != nil {
		_, _ = fmt.Fprintf(out, "Could not remove temporary update helper: %v\n", err)
	}
	if *noRestart {
		return nil
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

	helper, err := os.CreateTemp("", helperPrefix+"*.exe")
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

// sweepOrphanedHelpers deletes update helpers a previous run left in the system
// temporary directory. A helper that is still executing keeps its own file
// locked and is skipped, so this never disturbs a concurrent update.
func sweepOrphanedHelpers() {
	dir := os.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !isTemporaryHelper(path) {
			continue
		}
		_ = os.Remove(path)
	}
}

func processCreationTime(handle windows.Handle) (int64, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return creation.Nanoseconds(), nil
}

func waitForParentExit(parentPID int, parentStarted int64, out io.Writer) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(parentPID))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("open current Coddy process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	// Windows hands a freed PID to whichever process asks for one next. Without
	// this check the helper could sit waiting on an unrelated program that
	// inherited the number, and install the update far too late, or never.
	if parentStarted != 0 {
		started, err := processCreationTime(handle)
		if err != nil {
			return fmt.Errorf("read current Coddy start time: %w", err)
		}
		if started != parentStarted {
			_, _ = fmt.Fprintln(out, "Current Coddy process already exited.")
			return nil
		}
	}

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
	// The backup only has a job while the target is half replaced. Every exit
	// below either restores it or leaves the target whole, so none of them has
	// a reason to keep a second copy of the executable on disk.
	defer func() { _ = os.Remove(backup) }()

	_, _ = fmt.Fprintln(out, "Installing downloaded update...")
	if err := renameWithRetry(req.StagedPath, req.TargetPath, out); err != nil {
		_ = os.Remove(req.StagedPath)
		return fmt.Errorf("install update: %w; current Coddy was preserved at %s", err, req.TargetPath)
	}

	if req.Restart {
		_, _ = fmt.Fprintln(out, "Restarting Coddy...")
		if err := restartUpdatedCoddy(req.TargetPath, helperPath); err != nil {
			if restoreErr := restoreWindowsBackup(req.TargetPath, backup, out); restoreErr != nil {
				return fmt.Errorf("restart updated Coddy: %w; restore previous Coddy: %v", err, restoreErr)
			}
			return fmt.Errorf("restart updated Coddy: %w; restored the previous version", err)
		}
	} else {
		// Nobody is going to start Coddy again, but deleting this helper still
		// takes a process that is not this one. The install already succeeded,
		// so a helper that survives is litter, not a failure.
		cleanUpAfterInstall(req.TargetPath, helperPath)
	}
	_, _ = fmt.Fprintln(out, "Coddy update installed successfully.")
	return nil
}

// restartUpdatedCoddy starts the freshly installed executable. The preferred
// route hands it the helper path so it can delete the helper this process still
// runs from, which only builds carrying that handoff understand. Installing an
// older release - `coddy update --version` walking backwards - is a normal
// thing to do, so fall back to a plain restart and leave the helper for the
// next update to sweep.
func restartUpdatedCoddy(target, helperPath string) error {
	if helperPath != "" && restartProbe(target) {
		return startProcess(target, restartAfterUpdateCommand, "--helper", helperPath)
	}
	return startProcess(target)
}

// cleanUpAfterInstall asks the installed build to delete the helper without
// starting Coddy, for the `--no-restart` install that has nothing else to hand
// the job to. A release that predates the handoff leaves the helper for the
// next update to sweep.
func cleanUpAfterInstall(target, helperPath string) {
	if helperPath == "" || !restartProbe(target) {
		return
	}
	_ = startProcess(target, restartAfterUpdateCommand, "--helper", helperPath, "--no-restart")
}

func targetUnderstandsRestartHandoff(target string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, restartAfterUpdateCommand, "--probe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
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
	start := time.Now()
	announced := false
	for {
		err := renameFile(source, target)
		if err == nil {
			return nil
		}
		deadline, retryable := retryDeadlineFor(err)
		if !retryable || time.Since(start) > deadline {
			return describeRenameFailure(err, target)
		}
		if !announced {
			_, _ = fmt.Fprintln(out, "Waiting for Windows to release the executable lock...")
			announced = true
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// retryDeadlineFor reports how long a failed rename is worth retrying. Waiting
// out a sharing violation usually works; waiting out a permission failure only
// delays the report, so that one gets a much shorter budget.
func retryDeadlineFor(err error) (time.Duration, bool) {
	switch {
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION):
		return sharingViolationDeadline, true
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return accessDeniedDeadline, true
	default:
		return 0, false
	}
}

func describeRenameFailure(err error, target string) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w (Windows denied access to %s; installing there may need an elevated console)", err, filepath.Dir(target))
	}
	return err
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
		if _, retryable := retryDeadlineFor(err); !retryable || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func isTemporaryHelper(path string) bool {
	if filepath.Base(path) == "." || !strings.HasPrefix(strings.ToLower(filepath.Base(path)), helperPrefix) || !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return false
	}
	rel, err := filepath.Rel(os.TempDir(), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func restoreWindowsBackup(target, backup string, out io.Writer) error {
	failed := target + ".failed"
	if err := renameFile(target, failed); err != nil {
		return fmt.Errorf("move failed update aside: %w", err)
	}
	if err := renameWithRetry(backup, target, io.Discard); err != nil {
		return err
	}
	// The rejected build has no use once the working one is back. Failing to
	// delete it is not worth turning a successful rollback into an error.
	if err := os.Remove(failed); err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(out, "Could not remove the rejected update at %s: %v\n", failed, err)
	}
	return nil
}
