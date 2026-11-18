//go:build desktop && windows

package desktop

import (
	"os"
	"path/filepath"
)

// Where a Chromium-family browser lives on Windows, most preferred first.
//
// Three roots cover every ordinary installation: the two Program Files
// folders, where an installer puts a browser for the whole machine, and the
// user's local application data, where Chrome and Edge install themselves for
// one account without asking for administrator rights. The bare executable
// names are kept at the end for a browser on PATH - a portable copy, or one
// installed somewhere these roots do not reach.
//
// Firefox is deliberately absent, as it is elsewhere: it has no equivalent of
// --app.
//
// Windows gives the window its own frame and nothing more: the X11 half of
// this package - taking the frame off, cutting the corners, moving the window
// with the bar, grabbing the chords the browser answers itself - is not there,
// and the strip asks whether the window can be driven before it draws any of
// it. The console the binary is started from stays open behind the window,
// because `coddy` is one program for the terminal and the desktop both.
func browserCandidates() []string {
	relative := []string{
		`Google\Chrome\Application\chrome.exe`,
		`Chromium\Application\chrome.exe`,
		`Microsoft\Edge\Application\msedge.exe`,
		`BraveSoftware\Brave-Browser\Application\brave.exe`,
		`Vivaldi\Application\vivaldi.exe`,
		`Yandex\YandexBrowser\Application\browser.exe`,
	}
	var roots []string
	for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if dir := os.Getenv(name); dir != "" {
			roots = append(roots, dir)
		}
	}
	out := make([]string, 0, len(roots)*len(relative)+4)
	for _, root := range roots {
		for _, rel := range relative {
			out = append(out, filepath.Join(root, rel))
		}
	}
	// On PATH, for anything the roots above do not cover.
	return append(out, "chrome.exe", "msedge.exe", "brave.exe", "vivaldi.exe")
}
