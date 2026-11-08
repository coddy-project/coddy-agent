//go:build desktop && linux && !android

package desktop

import "testing"

// isolateAutostart points the entry that opens the window at login at a folder
// of this test's own, so a suite neither reads nor writes the autostart of the
// person running it.
func isolateAutostart(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}
