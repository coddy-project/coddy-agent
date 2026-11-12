//go:build desktop && darwin

package desktop

import (
	"os"
	"path/filepath"
)

// Where a Chromium-family browser lives on macOS, most preferred first.
//
// A browser is a bundle rather than a name on PATH, and --app has to reach the
// executable inside it, so these are paths and not names. Both of the places
// a bundle is installed are looked in: /Applications, where an installer puts
// it for everyone, and ~/Applications, where one can be dropped for a single
// account.
//
// Firefox is deliberately absent, as it is on Linux: it has no equivalent of
// --app, so the "window" would be an ordinary tab in the browser the operator
// reads mail in, with no process to wait on and the agent's cookie in their
// everyday profile.
//
// What macOS does not give the window is the rest of what X11 gives it on
// Linux: the frame stays, the corners are the system's, the bar does not move
// the window and no chord is taken from the browser. The strip draws itself
// accordingly (shell.MenuRows and the window buttons both ask whether the
// window can be driven at all).
func browserCandidates() []string {
	bundles := []string{
		"Google Chrome.app/Contents/MacOS/Google Chrome",
		"Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"Chromium.app/Contents/MacOS/Chromium",
		"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"Brave Browser.app/Contents/MacOS/Brave Browser",
		"Vivaldi.app/Contents/MacOS/Vivaldi",
		"Yandex.app/Contents/MacOS/Yandex",
	}
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	out := make([]string, 0, len(bundles)*len(roots))
	for _, root := range roots {
		for _, bundle := range bundles {
			out = append(out, filepath.Join(root, bundle))
		}
	}
	return out
}
