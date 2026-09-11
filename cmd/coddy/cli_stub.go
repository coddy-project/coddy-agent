//go:build !cli

package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/dryrun"
)

// runCLI reports that the interactive console is not compiled in. The two
// console flags that need no console, -t / --test-config and --dry-run, still
// work, so `coddy -t` and `coddy --dry-run` mean the same thing in every build.
func runCLI(args []string) error {
	if req, ok := leanCheckRequestFrom(args); ok {
		if req.dryRun {
			return runConsoleDryRun(req.cli, req.testConfig, req.remote, req.remoteToken, func(c *config.Config) error {
				if req.scheduler {
					c.Scheduler.Enabled = true
				}
				return nil
			})
		}
		return runConfigTest(req.cli)
	}
	return fmt.Errorf("interactive console is not built in (rebuild with: go build -tags=cli, or make build TAGS=cli)")
}

// leanCheckRequest is what the lean stub understands of the console flags: the
// paths a check needs, the two check flags, and the remote flags a dry run
// probes. Any other flag belongs to the console proper and leaves the stub
// error in place.
type leanCheckRequest struct {
	cli         config.CLIPaths
	dryRun      bool
	testConfig  bool
	remote      string
	remoteToken string
	scheduler   bool
}

func leanCheckRequestFrom(args []string) (leanCheckRequest, bool) {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", "")
	homeDir := fs.String("home", "", "")
	cwdFlag := fs.String("cwd", "", "")
	remoteFlag := fs.String("remote", "", "")
	remoteToken := fs.String("remote-token", "", "")
	scheduler := fs.Bool("scheduler", false, "")
	testConfig := config.AddCheckFlag(fs)
	dryRun := dryrun.AddFlag(fs)
	if err := fs.Parse(args); err != nil || (!*testConfig && !*dryRun) {
		return leanCheckRequest{}, false
	}
	return leanCheckRequest{
		cli: config.CLIPaths{
			Home:   strings.TrimSpace(*homeDir),
			CWD:    strings.TrimSpace(*cwdFlag),
			Config: strings.TrimSpace(*cfgPath),
		},
		dryRun:      *dryRun,
		testConfig:  *testConfig,
		remote:      strings.TrimSpace(*remoteFlag),
		remoteToken: strings.TrimSpace(*remoteToken),
		scheduler:   *scheduler,
	}, true
}

// cliInteractiveDefault keeps bare `coddy` on the usage path in lean builds
// without importing any terminal dependency.
func cliInteractiveDefault() bool { return false }
