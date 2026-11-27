//go:build windows

package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func TestWindowsUpdateServiceLifecycle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		state          serve.WindowsServiceState
		inspectErr     error
		stopErr        error
		cleanupStopErr error
		probeErr       error
		startErrors    []error
		wantEvents     []string
		wantBinary     string
		wantError      string
		wantWarning    string
	}{
		{name: "running service", state: serve.WindowsServiceState{Installed: true, Running: true}, wantEvents: []string{"inspect", "stop", "probe:next", "start:next"}, wantBinary: "next"},
		{name: "stopped service", state: serve.WindowsServiceState{Installed: true}, wantEvents: []string{"inspect", "probe:next"}, wantBinary: "next"},
		{name: "handoff fails", state: serve.WindowsServiceState{Installed: true, Running: true}, probeErr: errors.New("probe failed"), wantEvents: []string{"inspect", "stop", "probe:next", "start:previous"}, wantBinary: "previous", wantError: "restored the previous version"},
		{name: "new service fails", state: serve.WindowsServiceState{Installed: true, Running: true}, startErrors: []error{errors.New("start failed")}, wantEvents: []string{"inspect", "stop", "probe:next", "start:next", "stop", "start:previous"}, wantBinary: "previous", wantError: "restored the previous version"},
		{name: "failed new service cannot stop", state: serve.WindowsServiceState{Installed: true, Running: true}, startErrors: []error{errors.New("start timeout")}, cleanupStopErr: errors.New("still running"), wantEvents: []string{"inspect", "stop", "probe:next", "start:next", "stop"}, wantBinary: "next", wantError: "previous executable was not restored"},
		{name: "stop wait fails", state: serve.WindowsServiceState{Installed: true, Running: true}, stopErr: context.DeadlineExceeded, wantEvents: []string{"inspect", "stop", "start:previous"}, wantBinary: "previous", wantError: "stop Coddy Windows service before update"},
		{name: "stop wait and recovery fail", state: serve.WindowsServiceState{Installed: true, Running: true}, stopErr: context.DeadlineExceeded, startErrors: []error{errors.New("recovery failed")}, wantEvents: []string{"inspect", "stop", "start:previous"}, wantBinary: "previous", wantError: "stop Coddy Windows service before update", wantWarning: "recovery failed"},
		{name: "inspection fails", inspectErr: errors.New("inspection failed"), wantEvents: []string{"inspect"}, wantBinary: "previous", wantError: "inspection failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			target, staged := filepath.Join(dir, "coddy.exe"), filepath.Join(dir, "staged.exe")
			for path, content := range map[string]string{target: "previous", staged: "next"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var events []string
			readBinary := func() string {
				b, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			starts := 0
			stops := 0
			ops := windowsUpdateOps{
				serviceState: func(context.Context, string) (serve.WindowsServiceState, error) {
					events = append(events, "inspect")
					return tc.state, tc.inspectErr
				},
				stopService: func(context.Context, string) error {
					events = append(events, "stop")
					stops++
					if stops > 1 && tc.cleanupStopErr != nil {
						return tc.cleanupStopErr
					}
					return tc.stopErr
				},
				startService: func(context.Context, string) error {
					events = append(events, "start:"+readBinary())
					starts++
					if starts <= len(tc.startErrors) {
						return tc.startErrors[starts-1]
					}
					return nil
				},
				probe: func(string) (bool, error) { events = append(events, "probe:"+readBinary()); return false, tc.probeErr },
			}
			var out bytes.Buffer
			err := installWindowsUpdateWith(windowsUpdateRequest{TargetPath: target, StagedPath: staged}, "", &out, ops)
			if tc.wantError == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if tc.stopErr != nil && !errors.Is(err, tc.stopErr) {
				t.Errorf("error %v does not wrap stop error", err)
			}
			if tc.inspectErr != nil && !errors.Is(err, tc.inspectErr) {
				t.Errorf("error %v does not wrap inspection error", err)
			}
			if !reflect.DeepEqual(events, tc.wantEvents) {
				t.Errorf("events = %v, want %v", events, tc.wantEvents)
			}
			if got := readBinary(); got != tc.wantBinary {
				t.Errorf("binary = %q, want %q", got, tc.wantBinary)
			}
			if tc.wantWarning != "" && !strings.Contains(out.String(), tc.wantWarning) {
				t.Errorf("output = %q, want warning %q", out.String(), tc.wantWarning)
			}
		})
	}
}

func TestWindowsUpdateKeepsBackupWhenRestoreFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target, staged := filepath.Join(dir, "coddy.exe"), filepath.Join(dir, "staged.exe")
	for path, content := range map[string]string{target: "previous", staged: "next"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ops := windowsUpdateOps{
		serviceState: func(context.Context, string) (serve.WindowsServiceState, error) {
			return serve.WindowsServiceState{}, nil
		},
		probe: func(string) (bool, error) { return false, errors.New("probe failed") },
		restoreBackup: func(target, _ string, _ io.Writer) error {
			if err := os.Rename(target, target+".rejected"); err != nil {
				t.Fatal(err)
			}
			return errors.New("restore rename failed")
		},
	}
	err := installWindowsUpdateWith(windowsUpdateRequest{TargetPath: target, StagedPath: staged}, "", io.Discard, ops)
	if err == nil || !strings.Contains(err.Error(), "previous executable preserved at") {
		t.Fatalf("restore error = %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(dir, ".coddy-update-backup-*"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("preserved backups = %v, error = %v", backups, globErr)
	}
	content, readErr := os.ReadFile(backups[0])
	if readErr != nil || string(content) != "previous" {
		t.Fatalf("backup content = %q, error = %v", content, readErr)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target unexpectedly exists after the simulated failed restore: %v", statErr)
	}
}
