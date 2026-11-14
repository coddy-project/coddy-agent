//go:build desktop && !(darwin && cgo)

package desktop

import (
	"context"
	"errors"
)

// The Cocoa/WKWebView window needs macOS and cgo, which is what every macOS
// release archive is built with and no build tag asks for. Other platforms and
// cgo-free builds - cross-compiling turns cgo off by itself - keep the browser
// backend. Keep this constraint the exact complement of native_darwin.go under
// desktop.
const nativeAvailable = false

const nativeUnavailableHint = "desktop: this build has no native window - it comes from building on macOS with cgo; use --window browser, or leave --window at auto"

func openNativeWindow(_ context.Context, _ Options, _ string) error {
	return errors.New(nativeUnavailableHint)
}
