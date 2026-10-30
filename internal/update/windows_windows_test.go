//go:build windows

package update

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCopyUpdateHelperUsesSystemTemp(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "coddy.exe")
	if err := os.WriteFile(source, []byte("current Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper, err := copyUpdateHelper(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(helper) })

	rel, err := filepath.Rel(os.TempDir(), helper)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		t.Fatalf("helper %q is outside system temp %q", helper, os.TempDir())
	}
	got, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current Coddy" {
		t.Fatalf("helper contents = %q", got)
	}
}

func TestIsTemporaryHelper(t *testing.T) {
	t.Parallel()
	valid := filepath.Join(os.TempDir(), "coddy-update-helper-123.exe")
	if !isTemporaryHelper(valid) {
		t.Fatalf("isTemporaryHelper(%q) = false", valid)
	}
	for _, path := range []string{
		filepath.Join(os.TempDir(), "other.exe"),
		filepath.Join(filepath.Dir(os.TempDir()), "not-temp", "coddy-update-helper-123.exe"),
	} {
		if isTemporaryHelper(path) {
			t.Fatalf("isTemporaryHelper(%q) = true", path)
		}
	}
}

func TestInstallWindowsUpdateRestoresPreviousBinaryWhenTheNewBuildCannotStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "coddy.exe")
	staged := filepath.Join(dir, "coddy.new.exe")
	if err := os.WriteFile(target, []byte("previous Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("not an executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := installWindowsUpdate(windowsUpdateRequest{
		StagedPath: staged,
		TargetPath: target,
	}, "", &out)
	if err == nil {
		t.Fatal("expected a failure to start the new build")
	}
	if !strings.Contains(err.Error(), "restored the previous version") {
		t.Fatalf("error = %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "previous Coddy" {
		t.Fatalf("target after rollback = %q", got)
	}
	if entries, readErr := os.ReadDir(dir); readErr != nil {
		t.Fatal(readErr)
	} else if len(entries) != 1 || entries[0].Name() != "coddy.exe" {
		t.Fatalf("rollback left files behind: %v", names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

func TestRetryDeadlineFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		err       error
		want      time.Duration
		retryable bool
	}{
		{"sharing violation waits out the other process", windows.ERROR_SHARING_VIOLATION, sharingViolationDeadline, true},
		{"access denied is mostly a permission problem", windows.ERROR_ACCESS_DENIED, accessDeniedDeadline, true},
		{"a missing source never appears", os.ErrNotExist, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, retryable := retryDeadlineFor(&os.LinkError{Op: "rename", Err: tc.err})
			if retryable != tc.retryable || got != tc.want {
				t.Fatalf("retryDeadlineFor = (%v, %v), want (%v, %v)", got, retryable, tc.want, tc.retryable)
			}
		})
	}
}

func TestRenameWithRetryWaitsOutASharingViolation(t *testing.T) {
	restore := renameFile
	t.Cleanup(func() { renameFile = restore })

	calls := 0
	renameFile = func(string, string) error {
		calls++
		if calls < 3 {
			return &os.LinkError{Op: "rename", Err: windows.ERROR_SHARING_VIOLATION}
		}
		return nil
	}

	var out bytes.Buffer
	if err := renameWithRetry("staged", "target", &out); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("renamed %d times, want 3", calls)
	}
	if got := strings.Count(out.String(), "Waiting for Windows"); got != 1 {
		t.Fatalf("announced the wait %d times, want 1", got)
	}
}

func TestRenameWithRetryReportsAnElevationHintOnAccessDenied(t *testing.T) {
	restoreRename, restoreDeadline := renameFile, accessDeniedDeadline
	t.Cleanup(func() {
		renameFile = restoreRename
		accessDeniedDeadline = restoreDeadline
	})
	accessDeniedDeadline = 10 * time.Millisecond

	denied := &os.LinkError{Op: "rename", Err: windows.ERROR_ACCESS_DENIED}
	renameFile = func(string, string) error { return denied }

	target := filepath.Join(t.TempDir(), "coddy.exe")
	err := renameWithRetry("staged", target, &bytes.Buffer{})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("error = %v, want an access denied", err)
	}
	if !strings.Contains(err.Error(), "elevated console") {
		t.Fatalf("error does not explain the likely cause: %v", err)
	}
}

func TestInstallWindowsUpdateCleansUpWhenTheSwapFails(t *testing.T) {
	restore := renameFile
	t.Cleanup(func() { renameFile = restore })
	renameFile = func(string, string) error { return errors.New("the volume went away") }

	dir := t.TempDir()
	target := filepath.Join(dir, "coddy.exe")
	staged := filepath.Join(dir, ".coddy-update-staged")
	if err := os.WriteFile(target, []byte("previous Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("next Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := installWindowsUpdate(windowsUpdateRequest{
		StagedPath: staged,
		TargetPath: target,
	}, "", &out)
	if err == nil {
		t.Fatal("expected the install to fail")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "previous Coddy" {
		t.Fatalf("target = %q, want the untouched previous build", got)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 || entries[0].Name() != "coddy.exe" {
		t.Fatalf("failed install left files behind: %v", names(entries))
	}
}

func TestWaitForParentExitIgnoresARecycledPID(t *testing.T) {
	t.Parallel()
	// A live PID whose creation time does not match belongs to somebody else,
	// so the helper must not block on it. Waiting on this process really would
	// deadlock, which is exactly what the check has to prevent.
	var out bytes.Buffer
	if err := waitForParentExit(os.Getpid(), 1, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already exited") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunHelperAnswersTheHandoffProbe(t *testing.T) {
	t.Parallel()
	// The probe is how a helper asks whether the build it just installed
	// understands the handoff, so it has to succeed and do nothing else.
	handled, err := RunHelper([]string{afterUpdateCommand, "--probe"}, io.Discard)
	if !handled || err != nil {
		t.Fatalf("RunHelper(probe) = (%v, %v), want (true, nil)", handled, err)
	}
}

// A helper of an older release asks the build it installed to start again by
// leaving --no-restart out. That build is this one, so the first update to it
// must not open the console either.
func TestAfterUpdateNeverStartsCoddy(t *testing.T) {
	restore := startProcess
	t.Cleanup(func() { startProcess = restore })
	var started []string
	startProcess = func(target string, args ...string) error {
		started = append(started, target)
		return nil
	}

	for _, extra := range [][]string{nil, {"--no-restart"}} {
		helper, err := os.CreateTemp("", helperPrefix+"*.exe")
		if err != nil {
			t.Fatal(err)
		}
		if err := helper.Close(); err != nil {
			t.Fatal(err)
		}
		args := append([]string{afterUpdateCommand, "--helper", helper.Name()}, extra...)
		var out bytes.Buffer
		if handled, err := RunHelper(args, &out); !handled || err != nil {
			t.Fatalf("RunHelper(%v) = (%v, %v), want (true, nil)", args, handled, err)
		}
		if out.Len() != 0 {
			t.Fatalf("RunHelper(%v) said %q, want nothing", args, out.String())
		}
		if _, err := os.Stat(helper.Name()); !os.IsNotExist(err) {
			t.Fatalf("helper survived %v (stat err %v)", args, err)
		}
	}
	if len(started) != 0 {
		t.Fatalf("started %v, want nothing", started)
	}
}

// A release older than the handoff fails to parse the probe. It ran, which is
// all the helper needs to know, so that must not roll the install back.
func TestHandoffProbeTakesAFailingAnswerForAnOlderRelease(t *testing.T) {
	t.Parallel()
	// reg.exe is on every Windows and refuses an unknown operation with status 1.
	older := filepath.Join(os.Getenv("SystemRoot"), "System32", "reg.exe")
	handoff, err := installedBuildTakesHandoff(older)
	if err != nil || handoff {
		t.Fatalf("installedBuildTakesHandoff = (%v, %v), want (false, nil)", handoff, err)
	}
}

func TestHelperArgsNameTheRequest(t *testing.T) {
	t.Parallel()
	got := helperArgs(windowsUpdateRequest{ParentPID: 42, StagedPath: "staged", TargetPath: "target"}, 7)
	// The helper parses these back into the request it acts on, so a rename
	// here has to travel with the flag names of runWindowsUpdateHelper.
	for _, want := range []string{applyUpdateCommand, "--parent-pid", "42", "--parent-started", "7", "--source", "staged", "--target", "target"} {
		if !slices.Contains(got, want) {
			t.Fatalf("args = %v, missing %q", got, want)
		}
	}
}
