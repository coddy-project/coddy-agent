//go:build desktop && http && ui && linux && !android

package main

import (
	"os"
	"path/filepath"
)

func shortcutInteractiveUser() bool {
	return os.Geteuid() != 0 && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "")
}

func legacyShortcutInstall(exe string) bool {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return false
	}
	// Only migrate the default user layout from install.sh, not packages,
	// /usr/local, or a standalone binary copied into the user's bin directory.
	home = canonicalPath(home)
	if home == "" {
		return false
	}
	prefix := filepath.Join(home, ".local")
	if exe != filepath.Join(prefix, "bin", "coddy") {
		return false
	}
	for _, rel := range []string{"man/man1/coddy.1", "bash-completion/completions/coddy", "zsh/site-functions/_coddy"} {
		if info, err := os.Stat(filepath.Join(prefix, "share", filepath.FromSlash(rel))); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}
