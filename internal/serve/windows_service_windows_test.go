//go:build windows

package serve

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestDesiredWindowsServiceConfigUsesVirtualAccountAndPreservesSettings(t *testing.T) {
	t.Parallel()
	previous := mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartManual,
		ErrorControl:     mgr.ErrorSevere,
		LoadOrderGroup:   "group",
		Dependencies:     []string{"dependency"},
		DelayedAutoStart: true,
	}
	cfg := desiredWindowsServiceConfig(previous, `C:\Program Files\Coddy\coddy.exe`, []string{"__windows-service", "--home", `C:\Users\me\.coddy`})
	if cfg.ServiceStartName != WindowsServiceAccount {
		t.Fatalf("service account = %q, want %q", cfg.ServiceStartName, WindowsServiceAccount)
	}
	if cfg.ServiceStartName == "" || cfg.ServiceStartName == "LocalSystem" {
		t.Fatalf("unsafe service account %q", cfg.ServiceStartName)
	}
	if cfg.StartType != mgr.StartAutomatic || cfg.SidType != windows.SERVICE_SID_TYPE_UNRESTRICTED {
		t.Fatalf("service start/SID configuration = %d/%d", cfg.StartType, cfg.SidType)
	}
	if cfg.LoadOrderGroup != previous.LoadOrderGroup || cfg.DelayedAutoStart != previous.DelayedAutoStart || len(cfg.Dependencies) != 1 {
		t.Fatalf("existing settings were not preserved: %+v", cfg)
	}
	if !strings.HasPrefix(cfg.BinaryPathName, `"C:\Program Files\Coddy\coddy.exe" __windows-service`) {
		t.Fatalf("binary path = %q", cfg.BinaryPathName)
	}
}

func TestValidateWindowsServiceReconfigurationRefusesAnotherAccount(t *testing.T) {
	t.Parallel()
	exe := `C:\Program Files\Coddy\coddy.exe`
	for _, account := range []string{"", "LocalSystem", `.\coddy`} {
		cfg := mgr.Config{BinaryPathName: serviceImagePath(exe, nil), ServiceStartName: account}
		err := validateWindowsServiceReconfiguration(cfg, exe)
		if err == nil || !strings.Contains(err.Error(), "uninstall it explicitly") {
			t.Fatalf("account %q: error = %v", account, err)
		}
	}
	cfg := mgr.Config{BinaryPathName: serviceImagePath(exe, nil), ServiceStartName: `nt service\coddy`}
	if err := validateWindowsServiceReconfiguration(cfg, exe); err != nil {
		t.Fatalf("virtual account rejected: %v", err)
	}
}

func TestWindowsServiceElevationHint(t *testing.T) {
	t.Parallel()
	err := windowsServiceElevationHint(windows.ERROR_ACCESS_DENIED)
	if err == nil || !strings.Contains(err.Error(), "elevated PowerShell") || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("elevation error = %v", err)
	}
}

func TestWindowsServiceHandlerWaitsForReadiness(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	allowReady := make(chan struct{})
	h := windowsServiceHandler{run: func(ctx context.Context, _ []string, ready func()) error {
		close(entered)
		<-allowReady
		ready()
		<-ctx.Done()
		return nil
	}}
	requests := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 16)
	result := make(chan uint32, 1)
	go func() {
		_, code := h.Execute(nil, requests, changes)
		result <- code
	}()
	if status := <-changes; status.State != svc.StartPending {
		t.Fatalf("initial state = %v", status.State)
	}
	<-entered
	close(allowReady)
	if status := <-changes; status.State != svc.Running {
		t.Fatalf("state after readiness = %v", status.State)
	}
	select {
	case status := <-changes:
		t.Fatalf("readiness produced another transition: %v", status.State)
	case <-time.After(50 * time.Millisecond):
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if status := <-changes; status.State != svc.StopPending {
		t.Fatalf("state after stop = %v", status.State)
	}
	select {
	case code := <-result:
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not stop")
	}
}

func TestWindowsServiceHandlerReportsFailureBeforeReadiness(t *testing.T) {
	t.Parallel()
	h := windowsServiceHandler{run: func(context.Context, []string, func()) error {
		return errors.New("configuration failed")
	}}
	changes := make(chan svc.Status, 4)
	svcSpecific, code := h.Execute(nil, make(chan svc.ChangeRequest), changes)
	if !svcSpecific || code != 1 {
		t.Fatalf("Execute = (%v, %d), want (true, 1)", svcSpecific, code)
	}
	var states []svc.State
	for len(changes) > 0 {
		states = append(states, (<-changes).State)
	}
	for _, state := range states {
		if state == svc.Running {
			t.Fatal("failed service reported Running")
		}
	}
}

func TestWindowsServiceHandlerReturnsRestartExitCode(t *testing.T) {
	t.Parallel()
	h := windowsServiceHandler{run: func(_ context.Context, _ []string, ready func()) error {
		ready()
		return ExitCodeError{Code: ExitRestart, Err: ErrRestartRequested}
	}}
	changes := make(chan svc.Status, 4)
	svcSpecific, code := h.Execute(nil, make(chan svc.ChangeRequest), changes)
	if !svcSpecific || code != ExitRestart {
		t.Fatalf("Execute = (%v, %d), want (true, %d)", svcSpecific, code, ExitRestart)
	}
}

func TestGrantPathAccessAddsAnACE(t *testing.T) {
	t.Parallel()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := windows.StringToSid(current.Uid)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "service.txt")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := grantPathAccess(path, sid, windows.FILE_GENERIC_READ, false); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if aceSID.Equals(sid) && ace.Mask&windows.FILE_GENERIC_READ == windows.FILE_GENERIC_READ {
			return
		}
	}
	t.Fatalf("DACL %q does not contain %s", sd.String(), sid)
}
