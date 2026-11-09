//go:build desktop && (!linux || android)

package desktop

import (
	"errors"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// The tray icon is a StatusNotifierItem on a session message bus, which is
// what a Linux desktop runs. The window itself is ported to Linux only for
// now, and the icon goes with it: macOS and Windows each have a tray of their
// own, written when the window is.

type trayIcon struct{}

type trayMenu struct{}

func newTrayMenu(func() []shell.MenuItem, func(string) error) *trayMenu { return nil }

func newTrayIcon(*slog.Logger, func(), *trayMenu) *trayIcon { return nil }

func (t *trayIcon) Show() error {
	return errors.New("desktop: the tray icon is ported to Linux only for now")
}

func (t *trayIcon) Hide() {}

func (t *trayIcon) Visible() bool { return false }

func (t *trayIcon) menuChanged() {}
