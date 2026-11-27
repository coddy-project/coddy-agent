//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"golang.org/x/sys/windows/svc/eventlog"
)

var windowsServiceEvents *eventlog.Log

func traceServeStartup(eventID uint32) {
	if windowsServiceEvents != nil {
		_ = windowsServiceEvents.Info(eventID, "Coddy Windows service startup checkpoint")
	}
}

func runWindowsService(args []string) (retErr error) {
	events, err := eventlog.Open(serve.WindowsServiceName)
	windowsServiceEvents = events
	if err == nil {
		defer func() { _ = events.Close() }()
		defer func() {
			if retErr != nil {
				_ = events.Error(1, retErr.Error())
			}
		}()
	}
	if err := prepareWindowsServiceEnvironment(args); err != nil {
		return err
	}
	if events != nil {
		_ = events.Info(100, "Windows service environment prepared")
	}
	retErr = serve.RunWindowsService(serve.WindowsServiceName, args, func(ctx context.Context, serviceArgs []string, ready func()) error {
		err := runServeContext(ctx, serviceArgs, ready)
		if events != nil {
			_ = events.Info(101, fmt.Sprintf("Windows service serve loop returned: %v", err))
		}
		return err
	})
	if events != nil && retErr == nil {
		_ = events.Info(102, "Windows service SCM dispatcher returned without an error")
	}
	return retErr
}

func prepareWindowsServiceEnvironment(args []string) error {
	if err := os.Setenv(serve.EnvRole, serve.RoleService); err != nil {
		return fmt.Errorf("set Windows service role: %w", err)
	}
	if strings.TrimSpace(os.Getenv(config.EnvCODDYCWD)) != "" {
		return nil
	}
	fs := flag.NewFlagSet("windows-service", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	home := fs.String("home", "", "service home")
	_ = fs.String("config", "", "service config")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse Windows service paths: %w", err)
	}
	paths, err := config.Resolve(config.CLIPaths{Home: strings.TrimSpace(*home)})
	if err != nil {
		return fmt.Errorf("resolve Windows service home: %w", err)
	}
	if err := os.Setenv(config.EnvCODDYCWD, serve.WindowsServiceWorkspace(paths.Home)); err != nil {
		return fmt.Errorf("set Windows service workspace: %w", err)
	}
	return nil
}
