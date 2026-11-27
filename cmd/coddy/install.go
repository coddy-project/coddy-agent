//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func runWindowsServiceInstall(args []string, uninstall bool) error {
	home, configPath, err := parseInstallArgs(args, uninstall)
	if err != nil {
		if errors.Is(err, errInstallHelp) {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	exe, err := platform.Executable()
	if err != nil {
		return fmt.Errorf("locate coddy executable: %w", err)
	}
	if uninstall {
		if err := serve.UninstallWindowsService(ctx, exe, home); err != nil {
			return err
		}
		fmt.Println("Coddy Windows service is uninstalled; configuration, sessions and workspace were kept.")
		return nil
	}
	if err := config.RunCheck(os.Stdout, config.CLIPaths{Home: home, Config: configPath}); err != nil {
		return fmt.Errorf("check %s before installing the service: %w", configPath, err)
	}
	if err := serve.InstallWindowsService(ctx, exe, home, configPath); err != nil {
		return err
	}
	fmt.Printf("Coddy Windows service is installed and running as %s.\n  binary  %s\n  config  %s\n", serve.WindowsServiceAccount, exe, configPath)
	return nil
}

var errInstallHelp = errors.New("install help")

func parseInstallArgs(args []string, uninstall bool) (home, configPath string, err error) {
	name := "install"
	if uninstall {
		name = "uninstall"
	}
	f := newInstallFlagSet(name)
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return "", "", errInstallHelp
		}
		return "", "", e
	}
	if f.NArg() != 0 {
		return "", "", fmt.Errorf("coddy serve %s takes no arguments", name)
	}
	paths, e := config.Resolve(config.CLIPaths{Home: strings.TrimSpace(f.Home), Config: strings.TrimSpace(f.Config)})
	if e != nil {
		return "", "", e
	}
	return paths.Home, paths.ConfigPath, nil
}

type installFlags struct {
	*flag.FlagSet
	Home   string
	Config string
}

func newInstallFlagSet(name string) *installFlags {
	f := flag.NewFlagSet("coddy serve "+name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	out := &installFlags{FlagSet: f}
	f.StringVar(&out.Home, "home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	f.StringVar(&out.Config, "config", "", "path to config.yaml")
	f.Usage = func() {
		_, _ = fmt.Fprintf(f.Output(), "Usage: coddy serve %s [--home DIR] [--config PATH]\nWindows Service (systemd on Unix)\n", name)
	}
	return out
}
