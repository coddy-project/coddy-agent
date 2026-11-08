//go:build !(desktop && http && ui)

package main

import (
	"errors"
	"fmt"
)

// leanDesktopMessage is what a build without the window says of `coddy
// desktop`: the error of any run of it, and the answer to `coddy help
// desktop`, which must not fail in a build that answers every other question.
//
// The window needs the HTTP server it runs on and the SPA it opens, so any
// of the three tags missing takes the command away. This mirrors what
// cli_stub.go and metrics_stub.go do for their surfaces.
const leanDesktopMessage = "the desktop window is not built in (rebuild with: make build TAGS=\"http ui desktop\")"

func runDesktop(args []string) error {
	if wantsHelp(args) {
		fmt.Println(leanDesktopMessage)
		return nil
	}
	return errors.New(leanDesktopMessage)
}
