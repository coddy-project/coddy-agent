//go:build desktop && !(desktop.native && darwin && cgo)

package desktop

import (
	"context"
	"errors"
)

// The Cocoa/WKWebView window needs macOS, cgo and desktop.native. Other
// platforms and cgo-free builds retain the browser backend, including releases.
// Keep this constraint the exact complement of native_darwin.go under desktop.
const nativeAvailable = false

const nativeUnavailableHint = "desktop: this build has no native window - it needs macOS, cgo and the desktop.native tag; use --window browser, or leave --window at auto"

func openNativeWindow(_ context.Context, _ Options, _ string) error {
	return errors.New(nativeUnavailableHint)
}
