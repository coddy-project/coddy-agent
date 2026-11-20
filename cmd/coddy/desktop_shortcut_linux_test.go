//go:build desktop && http && ui && linux && !android

package main

import (
	"context"

	"errors"
	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyShortcutInstallLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exe := filepath.Join(home, ".local", "bin", "coddy")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if legacyShortcutInstall(exe) {
		t.Fatal("bare binary qualifies as a script installation")
	}
	man := filepath.Join(home, ".local", "share", "man", "man1", "coddy.1")
	if err := os.MkdirAll(filepath.Dir(man), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(man, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !legacyShortcutInstall(filepath.Join(canonicalPath(filepath.Dir(exe)), "coddy")) {
		t.Fatal("default install.sh layout with a companion man page is not recognised")
	}
	for _, other := range []string{"/usr/bin/coddy", "/usr/local/bin/coddy", filepath.Join(home, "Downloads", "coddy"), exe + ".test"} {
		if legacyShortcutInstall(other) {
			t.Errorf("%q unexpectedly qualifies", other)
		}
	}
}

func TestInstallDesktopShortcutDoesNotOpenOrConfigureCoddy(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	config := filepath.Join(root, "config")
	home := filepath.Join(root, "agent-home")
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", config)
	if err := runCommand([]string{"desktop", "--install-shortcut", "--home", home, "--config", filepath.Join(root, "missing.yaml")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent state directory was touched: %v", err)
	}
	entry, err := os.ReadFile(filepath.Join(data, "applications", "coddy-desktop.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "Exec=") || !strings.Contains(string(entry), " desktop\n") {
		t.Fatalf("unexpected launcher:\n%s", entry)
	}
	if _, err := os.Stat(filepath.Join(config, "coddy", "desktop-shortcut-v1")); err != nil {
		t.Fatalf("successful explicit install did not leave a receipt: %v", err)
	}
}

func TestRegisteredShortcutDeletionSurvivesDesktopLaunch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	path := filepath.Join(root, "data", "applications", "coddy-desktop.desktop")
	launch := func(folders []string) {
		t.Helper()
		err := desktop.Run(context.Background(), desktop.Options{Home: filepath.Join(root, "home"), RecentFolders: folders})
		if err == nil || !strings.Contains(err.Error(), "nothing was given to serve") {
			t.Fatalf("unexpected desktop launch result: %v", err)
		}
	}
	launch([]string{"/tmp/portable"})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("portable launch did not create entry: %v", err)
	}
	if err := runCommand([]string{"desktop", "--install-shortcut"}); err != nil {
		t.Fatal(err)
	}
	launch([]string{"/tmp/recent"})
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "--cwd /tmp/recent") {
		t.Fatalf("recent action missing: %s, %v", b, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	launch([]string{"/tmp/recent"})
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted launcher recreated: %v", err)
	}
	if err := runCommand([]string{"desktop", "--install-shortcut"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("repair did not restore launcher: %v", err)
	}
}
