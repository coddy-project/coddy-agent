//go:build desktop && (!linux || android)

package desktop

import (
	"errors"
	"log/slog"
)

// Only an X11 session can be asked to change a window it does not own, which
// is what a frameless window around a browser needs (window_x11.go). Every
// other browser backend keeps the system frame and draws no window buttons
// in the strip. The native macOS backend attaches its own window control,
// since that window belongs to this process.
var errWindowUnsupported = errors.New("desktop: this platform cannot be asked to change the window")

func newWindowControl(*slog.Logger, int) WindowControl { return noWindowControl{} }
