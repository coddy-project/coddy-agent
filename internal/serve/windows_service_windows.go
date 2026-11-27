//go:build windows

package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const WindowsServiceName = "Coddy"

var ErrWindowsService = errors.New("windows service is available only on Windows")

type WindowsServiceState struct {
	Installed bool
	Running   bool
	AutoStart bool
	ImagePath string
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

func serviceImagePath(exe string, args []string) string {
	parts := []string{syscall.EscapeArg(exe)}
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func InstallWindowsService(ctx context.Context, exe, home, config string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Service Control Manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	args := serviceArgs(home, config)
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		s, err = m.CreateService(WindowsServiceName, exe, mgr.Config{
			DisplayName: "Coddy Agent",
			Description: "Coddy agent server",
			StartType:   mgr.StartAutomatic,
		}, args...)
		if err != nil {
			return fmt.Errorf("create Windows service: %w", err)
		}
	} else {
		_, cfgErr := s.Config()
		if cfgErr != nil {
			_ = s.Close()
			return fmt.Errorf("read Windows service configuration: %w", cfgErr)
		}
		if err := s.UpdateConfig(mgr.Config{
			DisplayName:    "Coddy Agent",
			Description:    "Coddy agent server",
			StartType:      mgr.StartAutomatic,
			BinaryPathName: serviceImagePath(exe, args),
		}); err != nil {
			_ = s.Close()
			return fmt.Errorf("update Windows service: %w", err)
		}
		if serviceRunning(s) {
			_ = s.Close()
			return nil
		}
	}
	defer func() { _ = s.Close() }()
	if err := s.Start(); err != nil {
		return fmt.Errorf("start Windows service: %w", err)
	}
	return waitWindowsService(ctx, WindowsServiceName, svc.Running)
}

func serviceRunning(s *mgr.Service) bool {
	state, err := s.Query()
	return err == nil && state.State == svc.Running
}

func UninstallWindowsService(ctx context.Context, _ string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Service Control Manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return nil
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("stop Windows service: %w", err)
	}
	if err := waitWindowsService(ctx, WindowsServiceName, svc.Stopped); err != nil {
		return fmt.Errorf("wait for Windows service to stop: %w", err)
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete Windows service (it may still be stopping; retry after it stops): %w", err)
	}
	return nil
}

func WindowsServiceStateFor(_ context.Context, exe string) (WindowsServiceState, error) {
	m, err := mgr.Connect()
	if err != nil {
		return WindowsServiceState{}, err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return WindowsServiceState{}, nil
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
	return WindowsServiceState{Installed: sameServiceExecutable(cfg.BinaryPathName, exe), Running: st.State == svc.Running, ImagePath: cfg.BinaryPathName}, nil
}

func queryWindowsService() (svc.State, error) {
	m, err := mgr.Connect()
	if err != nil {
		return 0, err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return 0, err
	}
	defer func() { _ = s.Close() }()
	st, err := s.Query()
	if err != nil {
		return 0, err
	}
	return st.State, nil
}

func sameServiceExecutable(imagePath, exe string) bool {
	fields := strings.Fields(imagePath)
	if len(fields) == 0 || strings.TrimSpace(exe) == "" {
		return false
	}
	path := strings.Trim(fields[0], `"`)
	return strings.EqualFold(path, exe) || strings.Contains(strings.ToLower(imagePath), strings.ToLower(exe))
}

func StartWindowsService(ctx context.Context, _ string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if err := s.Start(); err != nil {
		return err
	}
	return waitWindowsService(ctx, WindowsServiceName, svc.Running)
}

func StopWindowsService(ctx context.Context, _ string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(WindowsServiceName)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return waitWindowsService(ctx, WindowsServiceName, svc.Stopped)
}

func waitWindowsService(ctx context.Context, _ string, want svc.State) error {
	deadline := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		state, err := queryWindowsService()
		if err != nil {
			return err
		}
		if state == want {
			return nil
		}
		if want == svc.Running && state == svc.Stopped {
			return fmt.Errorf("windows service stopped while waiting for it to start")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
		}
	}
}

func RunWindowsService(_ string, args []string, run func(context.Context, []string) error) error {
	return svc.Run(WindowsServiceName, windowsServiceHandler{args: args, run: run})
}

type windowsServiceHandler struct {
	args []string
	run  func(context.Context, []string) error
}

func (h windowsServiceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, h.args) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case req, ok := <-r:
			if !ok {
				return false, 1
			}
			switch req.Cmd {
			case svc.Interrogate:
				changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-done
				if err != nil {
					return true, 1
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		case err := <-done:
			if err != nil {
				changes <- svc.Status{State: svc.Stopped}
				return true, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
}
