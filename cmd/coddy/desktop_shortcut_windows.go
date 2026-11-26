//go:build desktop && http && ui

package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func shortcutInteractiveUser() bool {
	var session uint32
	return windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session) == nil && session != 0 &&
		!windows.GetCurrentProcessToken().IsElevated()
}

func legacyShortcutInstall(exe string) bool {
	local := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(local) {
		return false
	}
	local = canonicalPath(local)
	return local != "" && strings.EqualFold(exe, filepath.Join(local, "Programs", "coddy", "coddy.exe"))
}
