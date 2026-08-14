//go:build cli

package main

import (
	"github.com/EvilFreelancer/coddy-agent/external/cli"
)

// runCLI starts the interactive console surface (external/cli).
func runCLI(args []string) error {
	return cli.Run(args, cli.CommandDeps{
		EnsureHome: ensureCoddyHomeLayout,
		OpenStore:  openSessionStore,
	})
}

// cliInteractiveDefault reports whether bare `coddy` should open the console:
// only when both stdin and stdout are terminals.
func cliInteractiveDefault() bool {
	return cli.IsInteractiveTerminal()
}
