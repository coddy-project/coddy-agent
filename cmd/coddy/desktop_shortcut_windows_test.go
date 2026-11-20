//go:build desktop && http && ui

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyShortcutInstallWindows(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	dir := filepath.Join(local, "Programs", "coddy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(canonicalPath(dir), "coddy.exe")
	if !legacyShortcutInstall(strings.ToUpper(exe)) {
		t.Fatal("default install.ps1 layout is not recognised case-insensitively")
	}
	for _, other := range []string{filepath.Join(local, "Downloads", "coddy.exe"), filepath.Join(dir, "coddy-update-helper-test.exe"), exe + ".test"} {
		if legacyShortcutInstall(other) {
			t.Errorf("%q unexpectedly qualifies", other)
		}
	}
}
