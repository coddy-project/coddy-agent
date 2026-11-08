//go:build desktop && (!linux || android)

package desktop

import "errors"

// The window itself is ported to Linux only for now, and so is the entry that
// opens it at login: the XDG autostart folder is what a Linux session reads,
// and a macOS launch agent or a Windows startup entry is a different file in a
// different place, written when the window is ported there.

func setAutostart(bool) error {
	return errors.New("desktop: opening the window at login is ported to Linux only for now")
}

func autostartEnabled() bool { return false }
