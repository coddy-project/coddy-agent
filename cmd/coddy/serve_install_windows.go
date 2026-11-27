//go:build windows

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func runServeInstallCommand(args []string) error {
	return runWindowsServiceInstall(args, false)
}

func runServeUninstallCommand(args []string) error {
	return runWindowsServiceInstall(args, true)
}

func runServeStatusCommand(args []string) error {
	home, ok, err := controlHome(args, "status")
	if err != nil || !ok {
		return err
	}
	state, err := windowsServiceState()
	if err != nil {
		return err
	}
	if !state.ServesHome(home) {
		return runServeStatus(args)
	}
	fmt.Printf("Coddy Windows service is %s\n  auto start  %t\n  image       %s\n", windowsServiceRunning(state), state.AutoStart, state.ImagePath)
	return nil
}

func runServeStopCommand(args []string) error {
	home, ok, err := controlHome(args, "stop")
	if err != nil || !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	state, err := windowsServiceState()
	if err != nil {
		return err
	}
	if !state.ServesHome(home) {
		return runServeStop(args)
	}
	exe, err := platform.Executable()
	if err != nil {
		return err
	}
	if err := serve.StopWindowsService(ctx, exe); err != nil {
		return err
	}
	fmt.Println("Coddy Windows service stopped")
	return nil
}

func runServeRestartCommand(args []string) error {
	home, ok, err := controlHome(args, "restart")
	if err != nil || !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	state, err := windowsServiceState()
	if err != nil {
		return err
	}
	if !state.ServesHome(home) {
		return runServeRestart(args)
	}
	exe, err := platform.Executable()
	if err != nil {
		return err
	}
	if err := serve.StopWindowsService(ctx, exe); err != nil {
		return err
	}
	if err := serve.StartWindowsService(ctx, exe); err != nil {
		return err
	}
	fmt.Println("Coddy Windows service restarted")
	return nil
}

func windowsServiceState() (serve.WindowsServiceState, error) {
	exe, err := platform.Executable()
	if err != nil {
		return serve.WindowsServiceState{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return serve.WindowsServiceStateFor(ctx, exe)
}

func serviceServesHome(home string) bool {
	state, err := windowsServiceState()
	return err == nil && state.ServesHome(home)
}

func windowsServiceRunning(state serve.WindowsServiceState) string {
	if state.Running {
		return "running"
	}
	return "stopped"
}

func serviceHint() (string, bool) {
	state, err := windowsServiceState()
	if err != nil || !state.Installed {
		return "", false
	}
	return "Coddy is managed as a Windows service: use `coddy serve status`, `coddy serve stop`, `coddy serve restart` or `coddy serve uninstall`", state.Running
}
