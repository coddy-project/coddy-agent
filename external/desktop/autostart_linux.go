//go:build desktop && linux && !android

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Opening the window when the session starts is one file in one folder: the
// XDG autostart directory of this user, which every desktop environment reads
// on login. Nothing is installed system-wide and nothing needs root - the entry
// belongs to the person who switched it on, the same way the agent home does.

// autostartName is the entry's file name. It is the application's own id, so
// an entry written by the package and one written here are the same entry.
const autostartName = "coddy-desktop.desktop"

// autostartPath answers where the entry lives.
func autostartPath() (string, error) {
	dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("desktop: no home directory to put the autostart entry in: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", autostartName), nil
}

// setAutostart writes the entry or takes it away.
func setAutostart(on bool) error {
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("desktop: removing the autostart entry: %w", err)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("desktop: the autostart entry needs this program's own path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("desktop: the autostart folder: %w", err)
	}
	if err := os.WriteFile(path, []byte(autostartEntry(exe)), 0o600); err != nil {
		return fmt.Errorf("desktop: writing the autostart entry: %w", err)
	}
	return nil
}

// autostartEnabled reports whether the entry is there.
func autostartEnabled() bool {
	path, err := autostartPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// autostartEntry is the desktop entry itself.
//
// The Exec line is this program with the desktop verb after it: `coddy` is one
// binary for the terminal and the window both, and a bare path would open a
// console session at login instead of a window.
//
// The path is quoted the way the specification asks for it, because a program
// installed under a folder with a space in its name is still a program, and an
// unquoted Exec line would silently start something else.
func autostartEntry(exe string) string {
	return strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Coddy",
		"Comment=Coddy agent in a window",
		"Exec=" + quoteExec(exe) + " desktop",
		// The icon the package installs into the hicolor theme
		// (packaging/nfpm-desktop.yaml), so an entry written here draws the
		// same mark as the one in the application menu.
		"Icon=coddy-desktop",
		"Terminal=false",
		"Categories=Development;",
		"X-GNOME-Autostart-enabled=true",
		"",
	}, "\n")
}

// quoteExec quotes a program's path for an Exec line. Reserved characters are
// escaped with a backslash inside the quotes, as the desktop entry
// specification spells it out.
func quoteExec(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`") {
		return path
	}
	escaped := strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(path)
	return `"` + escaped + `"`
}
