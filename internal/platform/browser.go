package platform

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Openers a desktop Linux/BSD session may carry, with the arguments each one
// needs before the URL. wslview is the one that works inside WSL, where DISPLAY
// is set by WSLg but no Linux browser is installed; gio is the odd one out in
// wanting a subcommand, and calling it without one only prints its usage.
var browserOpeners = []struct {
	name string
	args []string
}{
	{name: "xdg-open"},
	{name: "x-www-browser"},
	{name: "www-browser"},
	{name: "gio", args: []string{"open"}},
	{name: "wslview"},
}

// LocalBrowserAvailable reports whether this machine plausibly has a browser
// the person at the keyboard is looking at.
//
// It answers one question only: should a command try to open a URL for the
// user, or just print it? Nothing else may depend on it - a login flow that
// changes shape based on this guess turns every false positive into a hung
// wait, which is exactly the failure this package is meant to avoid. A wrong
// answer here costs one stray opener process, or one URL the user opens by
// hand.
//
// An SSH session reads as "no browser" on every platform: the opener would
// raise a window on the far machine's console, where nobody is watching. X11
// forwarding is not carved out, because a forwarded display commonly has no
// browser behind it.
func LocalBrowserAvailable() bool {
	return localBrowserAvailable(runtime.GOOS, os.Getenv, exec.LookPath)
}

// BrowserOpenArgv returns the command that opens url in this machine's
// browser, or nil when there is nothing to open it with. It is the other half
// of LocalBrowserAvailable: the two agree on which openers count, so a probe
// that promises a browser is not followed by a command that cannot deliver one.
func BrowserOpenArgv(url string) []string {
	return browserOpenArgv(runtime.GOOS, os.Getenv, exec.LookPath, url)
}

func browserOpenArgv(goos string, getenv func(string) string, lookPath func(string) (string, error), url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	}
	if b := browserFromEnv(getenv); b != "" {
		return []string{b, url}
	}
	for _, opener := range browserOpeners {
		if _, err := lookPath(opener.name); err == nil {
			return append(append([]string{opener.name}, opener.args...), url)
		}
	}
	return nil
}

// browserFromEnv reads $BROWSER as a command to run, or "" when it names none
// this code can use. "none" is the opt-out; a %s template or a colon-separated
// list is more convention than practice, and running either verbatim would
// open nothing - so the probe and the opener both fall back to PATH instead.
func browserFromEnv(getenv func(string) string) string {
	b := strings.TrimSpace(getenv("BROWSER"))
	if b == "" || b == "none" || strings.Contains(b, ":") || strings.Contains(b, "%") {
		return ""
	}
	return b
}

func localBrowserAvailable(goos string, getenv func(string) string, lookPath func(string) (string, error)) bool {
	// The conventional opt-outs, honoured by other CLIs.
	if getenv("NO_BROWSER") != "" || getenv("BROWSER") == "none" {
		return false
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" || getenv("SSH_CLIENT") != "" {
		return false
	}
	switch goos {
	case "windows", "darwin":
		return true
	default:
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		if browserFromEnv(getenv) != "" {
			return true
		}
		for _, opener := range browserOpeners {
			if _, err := lookPath(opener.name); err == nil {
				return true
			}
		}
		return false
	}
}
