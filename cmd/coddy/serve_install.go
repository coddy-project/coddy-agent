package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// runServeInstall implements `coddy serve install`: the systemd user service of
// this account, written for a binary that came without a unit (the install
// script, a release archive, a local build) or taken from the package, then
// enabled and started.
func runServeInstall(args []string) error {
	if done, err := parseServiceVerb("install", "check ~/.coddy/config.yaml, install the systemd user unit for this binary when the package did not, enable coddy.service and start it working in ~/Coddy", args); done || err != nil {
		return err
	}
	svc, err := serve.NewUserService(os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return svc.Install(ctx)
}

// runServeUninstall implements `coddy serve uninstall`: the service stopped and
// disabled, and the unit install wrote removed. Configuration, sessions and the
// workspace stay where they are.
func runServeUninstall(args []string) error {
	if done, err := parseServiceVerb("uninstall", "stop and disable coddy.service and remove the unit that install wrote; ~/.coddy and ~/Coddy are kept", args); done || err != nil {
		return err
	}
	svc, err := serve.NewUserService(os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return svc.Uninstall(ctx)
}

// parseServiceVerb parses the arguments of a verb that takes none. It reports
// true when the caller only asked for the usage text.
func parseServiceVerb(verb, summary string, args []string) (bool, error) {
	fs := flag.NewFlagSet("serve "+verb, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage: coddy serve %s (%s)\n", verb, summary)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	if fs.NArg() != 0 {
		return false, fmt.Errorf("coddy serve %s takes no arguments", verb)
	}
	return false, nil
}

// serviceHint names the systemd user service when it serves this account, or
// will at the next login, for the daemon verbs, which only know about
// `coddy serve --daemon`. active reports whether it is running right now.
func serviceHint() (hint string, active bool) {
	active, enabled := serve.UserServiceState(context.Background())
	switch {
	case active:
		return "coddy serve runs as the systemd user service " + serve.UnitName +
			": systemctl --user status|stop|restart " + serve.UnitName + ", coddy serve uninstall to remove it", true
	case enabled:
		return "the systemd user service " + serve.UnitName + " is enabled for this account and starts at the next login or boot" +
			": systemctl --user start " + serve.UnitName + ", coddy serve uninstall to remove it", false
	}
	return "", false
}
