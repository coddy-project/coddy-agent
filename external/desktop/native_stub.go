//go:build desktop

package desktop

import (
	"context"
	"errors"
	"log/slog"
)

// The native window - a window of this process around the system WebView - is
// not in this tree yet. It needs cgo and the platform's WebView development
// packages (webkit2gtk-4.1 on Linux), which no build this repository publishes
// has: every release target but Android is cross-compiled with CGO_ENABLED=0.
// So it arrives behind a tag of its own, desktop.native, together with the
// build that proves it, and until then `--window native` says exactly that
// rather than opening nothing.
//
// This file has no partition against an implementation yet on purpose. When
// one lands it carries `desktop && desktop.native && cgo && <platforms>` and
// this stub takes the exact negation of that whole expression; written the
// other way round, a build with the sub-tag and no cgo would hold neither file.
const nativeAvailable = false

const nativeUnavailableHint = "desktop: this build has no native window - it needs the desktop.native tag, which needs cgo and the system WebView development packages; use --window browser, or leave --window at auto"

func openNativeWindow(_ context.Context, _ string, _ *slog.Logger) error {
	return errors.New(nativeUnavailableHint)
}
