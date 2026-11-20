//go:build desktop && http && ui && ((linux && !android) || windows)

package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopShortcutRelease(t *testing.T) {
	for v, want := range map[string]bool{"0.9.99": true, "v1.2.3": true, "dev": false, "v1.2.3-1-gabc": false, "v1.2.3-dirty": false} {
		if got := isShortcutRelease(v); got != want {
			t.Errorf("isShortcutRelease(%q) = %t, want %t", v, got, want)
		}
	}
}

func TestDesktopShortcutMigrationPreservesDeletion(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "coddy")
	receipt := filepath.Join(root, "config", "desktop-shortcut-v1")
	shortcut := filepath.Join(root, "shortcut")
	install := func(target string) error { return os.WriteFile(shortcut, []byte(target), 0o600) }

	if err := provisionDesktopShortcut(exe, receipt, false, install); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(shortcut); err != nil {
		t.Fatal(err)
	}
	if err := provisionDesktopShortcut(exe, receipt, false, install); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shortcut); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normal launch recreated the removed shortcut: %v", err)
	}
	if err := provisionDesktopShortcut(exe, receipt, true, install); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(shortcut); err != nil || string(got) != exe {
		t.Fatalf("explicit repair shortcut = %q, %v; want %q", got, err, exe)
	}
}

func TestDesktopShortcutMigrationRetriesFailure(t *testing.T) {
	receipt := filepath.Join(t.TempDir(), "desktop-shortcut-v1")
	fail := errors.New("shortcut directory unavailable")
	if err := provisionDesktopShortcut("coddy", receipt, false, func(string) error { return fail }); !errors.Is(err, fail) {
		t.Fatalf("got %v, want %v", err, fail)
	}
	called := false
	if err := provisionDesktopShortcut("coddy", receipt, false, func(string) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("migration did not retry: called=%t, err=%v", called, err)
	}
}

func TestDesktopShortcutFlag(t *testing.T) {
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	value := desktopShortcutFlag(fs)
	if err := fs.Parse([]string{"--install-shortcut"}); err != nil || !*value {
		t.Fatalf("flag not accepted: %v", err)
	}
	if err := runDesktop([]string{"--install-shortcut", "--test-config"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected the config check conflict, got %v", err)
	}
}
