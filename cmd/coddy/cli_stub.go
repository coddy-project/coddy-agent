//go:build !cli

package main

import "fmt"

// runCLI reports that the interactive console is not compiled in.
func runCLI(args []string) error {
	_ = args
	return fmt.Errorf("interactive console is not built in (rebuild with: go build -tags=cli, or make build TAGS=cli)")
}

// cliInteractiveDefault keeps bare `coddy` on the usage path in lean builds
// without importing any terminal dependency.
func cliInteractiveDefault() bool { return false }
