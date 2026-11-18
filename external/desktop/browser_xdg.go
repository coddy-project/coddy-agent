//go:build desktop && ((linux && !android) || freebsd)

package desktop

// browserCandidates are the Chromium-family browsers a Linux or FreeBSD
// desktop commonly has, most preferred first. They are looked up on PATH,
// which is where a package manager and a Flatpak wrapper both put them.
//
// Firefox is deliberately absent: it has no equivalent of --app, so the
// "window" would be an ordinary tab in the browser the operator reads mail in,
// with no process to wait on and the agent's cookie in their everyday profile -
// three of the four things this backend exists to avoid.
//
// Android satisfies the linux constraint too, which is why this file excludes
// it: Termux has no browser to start this way.
//
// The FreeBSD ports install the same browsers under different names, and none
// of the Linux spellings above exists there, so a FreeBSD desktop would find
// nothing without the tail of this list: www/chromium installs bin/chrome,
// www/ungoogled-chromium installs bin/ungoogled-chromium, www/brave installs
// bin/brave and www/iridium installs bin/iridium. They sit at the end because
// a Linux box that has one of the names above should keep preferring it, and
// a FreeBSD box matches none of them anyway.
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
		"chrome",
		"ungoogled-chromium",
		"brave",
		"iridium",
	}
}
