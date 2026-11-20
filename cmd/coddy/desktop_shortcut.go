//go:build desktop && http && ui && ((linux && !android) || windows)

package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/desktop"
	"github.com/EvilFreelancer/coddy-agent/internal/update"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func desktopShortcutFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("install-shortcut", false, "install or repair the application menu shortcut for this user, then exit")
}

func shortcutExecutable() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		// Self-update also replaces the symlink-resolved executable in place.
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", fmt.Errorf("locate Coddy executable for shortcut: %w", err)
	}
	return exe, nil
}

func installDesktopShortcut() error {
	exe, err := shortcutExecutable()
	if err != nil {
		return err
	}
	receipt, err := desktop.ShortcutReceiptPath()
	if err != nil {
		return fmt.Errorf("locate desktop shortcut receipt: %w", err)
	}
	return provisionDesktopShortcut(exe, receipt, true, desktop.InstallMenuShortcut)
}

// maybeMigrateDesktopShortcut is called only by real window and interactive
// console starts, never by services, protocol clients, checks or update helpers.
func maybeMigrateDesktopShortcut() {
	if !isShortcutRelease(version.Get()) || !shortcutInteractiveUser() {
		return
	}
	exe, err := shortcutExecutable()
	if err != nil || !legacyShortcutInstall(exe) {
		return
	}
	receipt, err := desktop.ShortcutReceiptPath()
	if err == nil {
		err = provisionDesktopShortcut(exe, receipt, false, desktop.InstallMenuShortcut)
	}
	if err != nil {
		slog.Debug("could not migrate desktop shortcut", "error", err)
	}
}

func isShortcutRelease(v string) bool {
	release := update.NormalizeSemver(v)
	return release != "" && strings.TrimPrefix(v, "v") == release
}

// The receipt is separate from the shortcut, so deleting the shortcut is not
// interpreted as an old installation to migrate. O_EXCL keeps two simultaneous
// first launches from both installing. Explicit repair always runs.
func provisionDesktopShortcut(exe, receipt string, explicit bool, install func(string) error) error {
	if err := os.MkdirAll(filepath.Dir(receipt), 0o700); err != nil {
		return fmt.Errorf("create desktop shortcut receipt directory: %w", err)
	}
	f, err := os.OpenFile(receipt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		if explicit {
			return install(exe)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create desktop shortcut receipt: %w", err)
	}
	if err := f.Close(); err != nil {
		return errors.Join(err, os.Remove(receipt))
	}
	if err := install(exe); err != nil {
		return errors.Join(err, os.Remove(receipt))
	}
	return nil
}

func canonicalPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolved
}
