//go:build !windows

package update

import (
	"errors"
	"io"
)

// helperCommandsAvailable keeps the private update commands off platforms that
// have no use for them; there `coddy __apply-update` is just an unknown
// argument.
var helperCommandsAvailable = false

// errNotWindows is returned by the Windows-only halves of the self-update flow
// when they are linked into a build for another platform.
var errNotWindows = errors.New("windows self-update can only run on windows")

func scheduleWindowsUpdate(windowsUpdateRequest) error {
	return errNotWindows
}

func runWindowsUpdateHelper(_ []string, _ io.Writer) error {
	return errNotWindows
}

func runWindowsRestartAfterUpdate(_ []string, _ io.Writer) error {
	return errNotWindows
}
