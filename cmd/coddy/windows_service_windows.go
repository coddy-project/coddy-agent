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
)

func runWindowsService(args []string) error {
	if err := prepareWindowsServiceEnvironment(args); err != nil {
		return err
	}
	return serve.RunWindowsService(serve.WindowsServiceName, args, func(ctx context.Context, serviceArgs []string, ready func()) error {
		return runServeContext(ctx, serviceArgs, ready)
	})
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
