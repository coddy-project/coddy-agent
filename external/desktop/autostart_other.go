//go:build desktop && !darwin && !windows && (!linux || android)

package desktop

import "errors"

// Every platform with no autostart of its own: the BSDs, Plan 9, and Android,
// where Termux has no login session to open a window in. Linux has the XDG
// autostart folder, macOS a launch agent and Windows a value under the Run
// key; there is nothing to write here, and the switch says so rather than
// pretending it took.

func setAutostart(bool) error {
	return errors.New("desktop: opening the window at login is written for Linux, macOS and Windows; this platform has no autostart Coddy knows")
}

func autostartEnabled() bool { return false }
