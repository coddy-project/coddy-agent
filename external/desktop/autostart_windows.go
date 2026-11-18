//go:build desktop && windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Opening the window at login on Windows is one value under the current
// user's Run key, which is what the shell reads when that account signs in.
// Nothing is written for the whole machine and nothing needs administrator
// rights - the entry belongs to the person who switched it on, the same way
// the agent home does.
//
// A shortcut in the Startup folder would be the other way, and it is worse
// here: a .lnk is a COM object to write, and it would have to be rewritten
// whenever the binary moves. A string value is a string.

// autostartRunKey is the key the value lives under. It is a variable so that
// a test can write somewhere harmless instead of the real Run key.
var autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValue is the name of the value. It is the application's name, so
// an entry written here and one a person reads in Task Manager are the same.
const autostartValue = "Coddy"

// setAutostart writes the value or takes it away.
func setAutostart(on bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("desktop: opening the Run key: %w", err)
	}
	defer func() { _ = key.Close() }()
	if !on {
		if err := key.DeleteValue(autostartValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
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
	if err := key.SetStringValue(autostartValue, autostartCommand(exe)); err != nil {
		return fmt.Errorf("desktop: writing the autostart entry: %w", err)
	}
	return nil
}

// autostartEnabled reports whether the value is there.
func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue(autostartValue)
	return err == nil && strings.TrimSpace(value) != ""
}

// autostartCommand is what the shell runs at login: this program, quoted
// because Program Files has a space in it, with the desktop verb after it.
func autostartCommand(exe string) string {
	return `"` + exe + `" desktop`
}
