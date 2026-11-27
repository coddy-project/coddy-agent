//go:build !windows

package serve

import (
	"context"
	"errors"
)

var ErrWindowsService = errors.New("windows service is available only on Windows")

type WindowsServiceState struct {
	Installed bool
	Running   bool
	AutoStart bool
	ImagePath string
	Home      string
}

func InstallWindowsService(_ context.Context, _, _, _ string) error { return ErrWindowsService }
func UninstallWindowsService(_ context.Context, _, _ string) error  { return ErrWindowsService }
func WindowsServiceStateFor(_ context.Context, _ string) (WindowsServiceState, error) {
	return WindowsServiceState{}, ErrWindowsService
}
func StartWindowsService(_ context.Context, _ string) error { return ErrWindowsService }
func StopWindowsService(_ context.Context, _ string) error  { return ErrWindowsService }
func RunWindowsService(_ string, _ []string, _ func(context.Context, []string, func()) error) error {
	return ErrWindowsService
}
