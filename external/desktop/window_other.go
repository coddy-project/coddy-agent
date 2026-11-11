//go:build desktop && (!linux || android)

package desktop

import (
	"errors"
	"log/slog"
)

// Only an X11 session can be asked to change a window it does not own, which
// is what a frameless window around a browser needs (window_x11.go). Every
// other platform keeps the system frame and draws no window buttons in the
// strip; the native window of the desktop.native tag is what closes the gap,
// since there the window belongs to this process.
var errWindowUnsupported = errors.New("desktop: this platform cannot be asked to change the window")

func newWindowControl(*slog.Logger, int) WindowControl { return noWindowControl{} }
