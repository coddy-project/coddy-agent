//go:build desktop && !darwin

package desktop

import (
	"context"
	"errors"
)

// The Cocoa/WKWebView window needs macOS, and nothing else: it reaches AppKit
// through the Objective-C runtime, so no build tag asks for it and no C
// compiler builds it. Every other platform keeps the browser backend. Keep
// this constraint the exact complement of native_darwin.go under desktop.
const nativeAvailable = false

const nativeUnavailableHint = "desktop: this build has no native window - it comes with every macOS build; use --window browser, or leave --window at auto"

func openNativeWindow(_ context.Context, _ Options, _ string) error {
	return errors.New(nativeUnavailableHint)
}
