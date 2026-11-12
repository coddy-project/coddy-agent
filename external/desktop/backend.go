//go:build desktop

package desktop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// The window backends, in the order `auto` prefers them.
const (
	// WindowAuto takes the native window when this binary has one and the
	// machine can open it, else a browser in app mode.
	WindowAuto = "auto"
	// WindowBrowser is a Chromium-family browser started with --app, in a
	// profile of its own so that it is a process this run can wait on.
	WindowBrowser = "browser"
	// WindowNative is a window around the system WebView (build tag
	// desktop.native, cgo).
	WindowNative = "native"
)

// ErrNoWaitableWindow is what `auto` answers when this machine has neither
// backend. It is not a failure of the run: the caller prints the address and
// serves until a signal, the way `coddy serve` does.
var ErrNoWaitableWindow = errors.New("desktop: no window this run can wait on")

// errNativeUnavailable is only a pre-window GUI/platform availability failure.
// Storage, thread and creation errors must remain failures of the native run.
var errNativeUnavailable = errors.New("desktop: the native window is unavailable")

// openWindow opens url and returns once that window has closed.
//
// Every backend here is one whose process this run can wait on, and that is
// the whole admission rule. Handing the URL to a browser that is already
// running - what platform.OpenBrowser does - returns in milliseconds whether
// or not a window ever appears, so a run whose lifetime is "until the window
// closes" would end as soon as the chat came up. platform.LocalBrowserAvailable
// is no help either: its own documentation says nothing may depend on it,
// because it answers true on Android whenever termux-open-url is on PATH and
// true unconditionally on Windows and macOS.
func openWindow(ctx context.Context, opts Options, url string) error {
	openNative := opts.nativeOpen
	if openNative == nil && nativeAvailable {
		openNative = openNativeWindow
	}
	switch opts.Window {
	case WindowNative:
		if openNative == nil {
			return errors.New(nativeUnavailableHint)
		}
		return openNative(ctx, opts, url)
	case WindowBrowser:
		return openBrowserWindow(ctx, opts, url)
	case "", WindowAuto:
		if openNative != nil {
			if err := openNative(ctx, opts, url); err == nil {
				return nil
			} else if errors.Is(err, errNativeUnavailable) {
				logger(opts.Log).Warn("the native window is unavailable; trying the browser", "error", err)
			} else {
				return err
			}
		}
		err := openBrowserWindow(ctx, opts, url)
		if errors.Is(err, errNoBrowser) {
			return ErrNoWaitableWindow
		}
		return err
	default:
		return fmt.Errorf("desktop: --window %q is not one of %s, %s or %s", opts.Window, WindowAuto, WindowBrowser, WindowNative)
	}
}

// logger answers a logger that is always safe to call.
func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}
