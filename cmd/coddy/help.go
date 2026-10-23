package main

// `coddy help` and `coddy help <command> ...` (issue #453) - what many CLIs
// accept and what people type first. `coddy help` prints the command list
// `coddy --help` prints; `coddy help <command> ...` answers what
// `coddy <command> ... --help` answers.

import (
	"fmt"
	"os"
)

// unknownCommandError names a word that is not a command. The caller prints it
// with the usage text, so `coddy help <word>` answers like `coddy <word>`.
type unknownCommandError struct{ name string }

func (e *unknownCommandError) Error() string {
	return fmt.Sprintf("unknown command %q", e.name)
}

// wantsHelp reports whether the arguments ask for a usage screen with -h or
// --help, wherever the flag stands.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// runHelp prints the usage text, or forwards what follows to that command with
// --help appended: `coddy help serve install` is `coddy serve install --help`.
// A command line that already carries the flag runs as it stands, so it never
// gains a second one.
func runHelp(args []string) error {
	// `coddy help`, `coddy help help` and `coddy help --help` all name this
	// screen, the command list.
	if len(args) == 0 || args[0] == "help" || wantsHelp(args[:1]) {
		printUsage(os.Stdout)
		return nil
	}
	if wantsHelp(args) {
		return runCommand(args)
	}
	return runCommand(append(append([]string{}, args...), "--help"))
}
