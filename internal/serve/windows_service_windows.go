//go:build windows

package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	WindowsServiceName    = "Coddy"
	WindowsServiceAccount = `NT SERVICE\` + WindowsServiceName
)

var ErrWindowsService = errors.New("windows service is available only on Windows")

type WindowsServiceState struct {
	Installed bool
	Running   bool
	AutoStart bool
	ImagePath string
	Home      string
}

func serviceArgs(home, config string) []string {
	args := []string{"__windows-service"}
	if strings.TrimSpace(home) != "" {
		args = append(args, "--home", home)
	}
	if strings.TrimSpace(config) != "" {
		args = append(args, "--config", config)
	}
	return args
}

// WindowsServiceWorkspace is the default working directory of the SCM process.
func WindowsServiceWorkspace(home string) string {
	return filepath.Join(home, "workspace")
}

func serviceImagePath(exe string, args []string) string {
	parts := []string{syscall.EscapeArg(exe)}
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func openWindowsServiceManager(access uint32) (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, access)
	if err != nil {
		return nil, err
	}
	return &mgr.Mgr{Handle: h}, nil
}

func openWindowsService(m *mgr.Mgr, access uint32) (*mgr.Service, error) {
	name, err := windows.UTF16PtrFromString(WindowsServiceName)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(m.Handle, name, access)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: WindowsServiceName, Handle: h}, nil
}

func desiredWindowsServiceConfig(cfg mgr.Config, exe string, args []string) mgr.Config {
	cfg.ServiceType = windows.SERVICE_WIN32_OWN_PROCESS
	cfg.StartType = mgr.StartAutomatic
	cfg.ErrorControl = mgr.ErrorNormal
	cfg.BinaryPathName = serviceImagePath(exe, args)
	cfg.DisplayName = "Coddy Agent"
	cfg.Description = "Coddy agent server"
	cfg.ServiceStartName = WindowsServiceAccount
	cfg.Password = ""
	cfg.SidType = windows.SERVICE_SID_TYPE_UNRESTRICTED
	return cfg
}

func validateWindowsServiceReconfiguration(cfg mgr.Config, exe string) error {
	if !sameServiceExecutable(cfg.BinaryPathName, exe) {
		return fmt.Errorf("refusing to reconfigure service %s: its executable is %q, not %q", WindowsServiceName, cfg.BinaryPathName, exe)
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.ServiceStartName), WindowsServiceAccount) {
		return fmt.Errorf("refusing to reconfigure service %s running as %q: uninstall it explicitly, then install it again as %s", WindowsServiceName, cfg.ServiceStartName, WindowsServiceAccount)
	}
	return nil
}

func configureWindowsServiceRecovery(s *mgr.Service) error {
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 24*60*60); err != nil {
		return fmt.Errorf("configure Windows service recovery: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("enable Windows service recovery on exit codes: %w", err)
	}
	return nil
}

func windowsServiceElevationHint(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w (run this command from an elevated PowerShell)", err)
	}
	return err
}

func InstallWindowsService(ctx context.Context, exe, home, config string) (retErr error) {
	defer func() { retErr = windowsServiceElevationHint(retErr) }()
	if err := EnsureHomeLayout(home); err != nil {
		return fmt.Errorf("prepare Windows service home: %w", err)
	}
	if err := os.MkdirAll(WindowsServiceWorkspace(home), 0o755); err != nil {
		return fmt.Errorf("prepare Windows service workspace: %w", err)
	}
	m, err := openWindowsServiceManager(windows.SC_MANAGER_CONNECT | windows.SC_MANAGER_CREATE_SERVICE)
	if err != nil {
		return fmt.Errorf("connect to Service Control Manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	args := serviceArgs(home, config)
	s, err := openWindowsService(m, windows.SERVICE_ALL_ACCESS)
	created := false
	safeToRollback := true
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		s, err = m.CreateService(WindowsServiceName, exe, desiredWindowsServiceConfig(mgr.Config{}, exe, args), args...)
		created = err == nil
	}
	if err != nil {
		return fmt.Errorf("open or create Windows service: %w", err)
	}
	defer func() { _ = s.Close() }()
	if created {
		defer func() {
			if retErr != nil && safeToRollback {
				_ = s.Delete()
			}
		}()
	}

	var previous mgr.Config
	wasRunning := false
	if !created {
		previous, err = s.Config()
		if err != nil {
			return fmt.Errorf("read Windows service configuration: %w", err)
		}
		if err := validateWindowsServiceReconfiguration(previous, exe); err != nil {
			return err
		}
		defer func() {
			if retErr == nil || !safeToRollback {
				return
			}
			if restoreErr := s.UpdateConfig(previous); restoreErr != nil {
				retErr = fmt.Errorf("%w; restore previous service configuration: %v", retErr, restoreErr)
				return
			}
			if wasRunning {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				if waitErr := waitWindowsService(cleanupCtx, s, svc.Stopped); waitErr != nil {
					retErr = fmt.Errorf("%w; reconcile previous service stop: %v", retErr, waitErr)
					return
				}
				if restartErr := s.Start(); restartErr != nil {
					retErr = fmt.Errorf("%w; restart previous service: %v", retErr, restartErr)
				}
			}
		}()
		st, queryErr := s.Query()
		if queryErr != nil {
			return fmt.Errorf("query Windows service: %w", queryErr)
		}
		if st.State == svc.StartPending || st.State == svc.StopPending {
			if waitErr := waitWindowsService(ctx, s, svc.Running, svc.Stopped); waitErr != nil {
				return fmt.Errorf("wait for Windows service pending state: %w", waitErr)
			}
			st, queryErr = s.Query()
			if queryErr != nil {
				return fmt.Errorf("query Windows service after pending state: %w", queryErr)
			}
		}
		wasRunning = st.State == svc.Running
		if wasRunning {
			if _, stopErr := s.Control(svc.Stop); stopErr != nil && !errors.Is(stopErr, windows.ERROR_SERVICE_NOT_ACTIVE) {
				return fmt.Errorf("stop Windows service before reinstall: %w", stopErr)
			}
			if waitErr := waitWindowsService(ctx, s, svc.Stopped); waitErr != nil {
				return fmt.Errorf("wait for Windows service before reinstall: %w", waitErr)
			}
		}
	}

	if err := s.UpdateConfig(desiredWindowsServiceConfig(previous, exe, args)); err != nil {
		return fmt.Errorf("update Windows service: %w", err)
	}
	// The service runs as its own low-privilege virtual account, not as
	// LocalSystem. Grant that account only the installation and state it needs.
	if err := grantWindowsServiceAccess(exe, home, config); err != nil {
		return fmt.Errorf("grant Windows service account access: %w", err)
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start Windows service: %w", err)
	}
	defer func() {
		if retErr == nil {
			return
		}
		// The readiness wait may have ended because its context was cancelled.
		// Cleanup gets its own deadline so Delete or rollback never runs while
		// the newly started process is still alive.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, stopErr := s.Control(svc.Stop); stopErr != nil && !errors.Is(stopErr, windows.ERROR_SERVICE_NOT_ACTIVE) {
			safeToRollback = false
			retErr = fmt.Errorf("%w; stop failed Windows service before cleanup: %v", retErr, stopErr)
			return
		}
		if waitErr := waitWindowsService(cleanupCtx, s, svc.Stopped); waitErr != nil {
			safeToRollback = false
			retErr = fmt.Errorf("%w; wait for failed Windows service before cleanup: %v", retErr, waitErr)
		}
	}()
	if err := waitWindowsService(ctx, s, svc.Running); err != nil {
		return fmt.Errorf("wait for Windows service to start: %w", err)
	}
	if err := configureWindowsServiceRecovery(s); err != nil {
		return err
	}
	return nil
}

func UninstallWindowsService(ctx context.Context, exe, home string) (retErr error) {
	defer func() { retErr = windowsServiceElevationHint(retErr) }()
	m, err := openWindowsServiceManager(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("connect to Service Control Manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	s, err := openWindowsService(m, windows.SERVICE_ALL_ACCESS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Windows service: %w", err)
	}
	defer func() { _ = s.Close() }()
	cfg, err := s.Config()
	if err != nil {
		return fmt.Errorf("read Windows service configuration: %w", err)
	}
	if !sameServiceExecutable(cfg.BinaryPathName, exe) {
		return fmt.Errorf("refusing to remove service %s: its executable is %q, not %q", WindowsServiceName, cfg.BinaryPathName, exe)
	}
	if registeredHome := windowsServiceHome(cfg.BinaryPathName); registeredHome == "" || !sameWindowsPath(registeredHome, home) {
		return fmt.Errorf("refusing to remove service %s: its home is %q, not %q", WindowsServiceName, registeredHome, home)
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("stop Windows service: %w", err)
	}
	if err := waitWindowsService(ctx, s, svc.Stopped); err != nil {
		return fmt.Errorf("wait for Windows service to stop: %w", err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete Windows service (it may still be stopping; retry after it stops): %w", err)
	}
	return nil
}

func WindowsServiceStateFor(_ context.Context, exe string) (WindowsServiceState, error) {
	m, err := openWindowsServiceManager(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return WindowsServiceState{}, err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := openWindowsService(m, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return WindowsServiceState{}, nil
	}
	if err != nil {
		return WindowsServiceState{}, err
	}
	defer func() { _ = s.Close() }()
	cfg, err := s.Config()
	if err != nil {
		return WindowsServiceState{}, err
	}
	st, err := s.Query()
	if err != nil {
		return WindowsServiceState{}, err
	}
	return WindowsServiceState{
		Installed: sameServiceExecutable(cfg.BinaryPathName, exe),
		Running:   st.State == svc.Running,
		AutoStart: cfg.StartType == mgr.StartAutomatic,
		ImagePath: cfg.BinaryPathName,
		Home:      windowsServiceHome(cfg.BinaryPathName),
	}, nil
}

func StartWindowsService(ctx context.Context, exe string) (retErr error) {
	defer func() { retErr = windowsServiceElevationHint(retErr) }()
	m, err := openWindowsServiceManager(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := openWindowsService(m, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS|windows.SERVICE_START)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if err := checkWindowsServiceExecutable(s, exe); err != nil {
		return err
	}
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return waitWindowsService(ctx, s, svc.Running)
}

func StopWindowsService(ctx context.Context, exe string) (retErr error) {
	defer func() { retErr = windowsServiceElevationHint(retErr) }()
	m, err := openWindowsServiceManager(windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := openWindowsService(m, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS|windows.SERVICE_STOP)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if err := checkWindowsServiceExecutable(s, exe); err != nil {
		return err
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return waitWindowsService(ctx, s, svc.Stopped)
}

func checkWindowsServiceExecutable(s *mgr.Service, exe string) error {
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	if !sameServiceExecutable(cfg.BinaryPathName, exe) {
		return fmt.Errorf("service %s belongs to %q, not %q", WindowsServiceName, cfg.BinaryPathName, exe)
	}
	return nil
}

func waitWindowsService(ctx context.Context, s *mgr.Service, wants ...svc.State) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := s.Query()
		if err != nil {
			return err
		}
		for _, want := range wants {
			if state.State == want {
				return nil
			}
		}
		if len(wants) == 1 && wants[0] == svc.Running && state.State == svc.Stopped {
			return fmt.Errorf("windows service stopped while waiting for it to start (Win32 exit code %d, service-specific exit code %d)", state.Win32ExitCode, state.ServiceSpecificExitCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func RunWindowsService(name string, args []string, run func(context.Context, []string, func()) error) error {
	return svc.Run(name, windowsServiceHandler{args: args, run: run})
}

type windowsServiceHandler struct {
	args []string
	run  func(context.Context, []string, func()) error
}

func (h windowsServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes <- svc.Status{State: svc.StartPending, WaitHint: 30000, CheckPoint: 1}
	ready := make(chan struct{})
	var readyOnce sync.Once
	done := make(chan error, 1)
	go func() {
		done <- h.run(ctx, h.args, func() { readyOnce.Do(func() { close(ready) }) })
	}()

	state := svc.StartPending
	var checkpoint uint32 = 1
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ready:
			// The readiness callback closes this channel. Disable it after the
			// one-shot transition or the select would spin on it forever.
			ready = nil
			if state == svc.StartPending {
				state = svc.Running
				checkpoint = 0
				changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			}
		case req, ok := <-requests:
			if !ok {
				cancel()
				return true, 1
			}
			switch req.Cmd {
			case svc.Interrogate:
				changes <- svc.Status{State: state, Accepts: svc.AcceptStop | svc.AcceptShutdown, WaitHint: 30000, CheckPoint: checkpoint}
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				if state != svc.StopPending {
					state = svc.StopPending
					checkpoint = 1
					cancel()
					changes <- svc.Status{State: svc.StopPending, Accepts: svc.AcceptPreShutdown, WaitHint: 30000, CheckPoint: checkpoint}
				}
			}
		case <-ticker.C:
			if state == svc.StartPending || state == svc.StopPending {
				checkpoint++
				changes <- svc.Status{State: state, WaitHint: 30000, CheckPoint: checkpoint}
			}
		case err := <-done:
			changes <- svc.Status{State: svc.Stopped}
			var coded ExitCodeError
			if errors.As(err, &coded) && coded.Code != 0 {
				return true, uint32(coded.Code) //nolint:gosec // G115: process exit codes fit in the SCM status field
			}
			if err != nil {
				return true, 1
			}
			return false, 0
		}
	}
}
