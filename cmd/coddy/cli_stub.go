//go:build !cli

package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// runCLI reports that the interactive console is not compiled in. The one
// console flag that needs no console, -t / --test-config, still works, so
// `coddy -t` means the same thing in every build.
func runCLI(args []string) error {
	if cli, ok := leanConfigTestRequest(args); ok {
		return runConfigTest(cli)
	}
	return fmt.Errorf("interactive console is not built in (rebuild with: go build -tags=cli, or make build TAGS=cli)")
}

// leanConfigTestRequest parses the console flags a config check needs. Any
// other flag belongs to the console proper and leaves the stub error in place.
func leanConfigTestRequest(args []string) (config.CLIPaths, bool) {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", "")
	homeDir := fs.String("home", "", "")
	cwdFlag := fs.String("cwd", "", "")
	testConfig := config.AddCheckFlag(fs)
	if err := fs.Parse(args); err != nil || !*testConfig {
		return config.CLIPaths{}, false
	}
	return config.CLIPaths{
		Home:   strings.TrimSpace(*homeDir),
		CWD:    strings.TrimSpace(*cwdFlag),
		Config: strings.TrimSpace(*cfgPath),
	}, true
}

// cliInteractiveDefault keeps bare `coddy` on the usage path in lean builds
// without importing any terminal dependency.
func cliInteractiveDefault() bool { return false }
