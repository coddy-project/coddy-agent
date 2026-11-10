//go:build desktop && linux && !android

package desktop

// browserCandidates are the Chromium-family browsers a Linux desktop commonly
// has, most preferred first. They are looked up on PATH, which is where a
// package manager and a Flatpak wrapper both put them.
//
// Firefox is deliberately absent: it has no equivalent of --app, so the
// "window" would be an ordinary tab in the browser the operator reads mail in,
// with no process to wait on and the agent's cookie in their everyday profile -
// three of the four things this backend exists to avoid.
//
// Android satisfies the linux constraint too, which is why this file excludes
// it: Termux has no browser to start this way.
func browserCandidates() []string {
	return []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
		"microsoft-edge-stable",
		"brave-browser",
		"vivaldi-stable",
		"vivaldi",
		"yandex-browser",
		"yandex-browser-stable",
	}
}
