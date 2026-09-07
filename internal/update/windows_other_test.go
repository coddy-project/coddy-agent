//go:build !windows

package update

import (
	"io"
	"testing"
)

func TestRunHelperLeavesPrivateCommandsToTheCallerOffWindows(t *testing.T) {
	t.Parallel()
	for _, command := range []string{applyUpdateCommand, restartAfterUpdateCommand} {
		handled, err := RunHelper([]string{command}, io.Discard)
		if handled || err != nil {
			t.Fatalf("RunHelper(%q) = (%v, %v), want the caller to report an unknown command", command, handled, err)
		}
	}
}
