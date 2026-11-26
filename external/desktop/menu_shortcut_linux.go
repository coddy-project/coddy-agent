//go:build linux && !android

package desktop

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/coddy-icon.png
var shortcutIcon []byte

// InstallMenuShortcut installs a per-user launcher for exe, retaining any
// recent-folder actions written by the desktop window.
func InstallMenuShortcut(exe string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("desktop: executable path must be absolute: %q", exe)
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" || !filepath.IsAbs(data) {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("desktop: find home: %w", err)
		}
		data = filepath.Join(home, ".local", "share")
	}
	icon := filepath.Join(data, "icons", "hicolor", "128x128", "apps", "coddy-desktop.png")
	path := filepath.Join(data, "applications", "coddy-desktop.desktop")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("desktop: read shortcut: %w", err)
	}
	quote := func(s string) string {
		s = strings.ReplaceAll(s, "%", "%%")
		s = strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", "$", "\\$").Replace(s)
		return `"` + s + `"`
	}
	entry := "[Desktop Entry]\nType=Application\nName=Coddy\nGenericName=Coding agent\nComment=Coddy agent in a window\nExec=" + quote(exe) + " desktop\nIcon=coddy-desktop\nTerminal=false\nCategories=Development;IDE;\nKeywords=coddy;agent;llm;ai;coding;assistant;\nStartupNotify=true\nStartupWMClass=coddy-desktop\n"
	// Retain only the known recent-folder actions, and retarget their commands.
	var actions string
	for _, line := range strings.Split(string(old), "\n") {
		if strings.HasPrefix(line, "Actions=") {
			entry += line + "\n"
		}
		if strings.HasPrefix(line, "[Desktop Action ") {
			actions += "\n"
		}
		if actions != "" {
			if i := strings.Index(line, " desktop --new-chat --cwd "); strings.HasPrefix(line, "Exec=") && i >= 0 {
				line = "Exec=" + quote(exe) + line[i:]
			}
			actions += line + "\n"
		}
	}
	for _, dir := range []string{filepath.Dir(icon), filepath.Dir(path)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("desktop: create shortcut directory: %w", err)
		}
	}
	if err := os.WriteFile(icon, shortcutIcon, 0o644); err != nil {
		return fmt.Errorf("desktop: write shortcut icon: %w", err)
	}
	if err := os.WriteFile(path, []byte(entry+actions), 0o644); err != nil {
		return fmt.Errorf("desktop: write shortcut: %w", err)
	}
	return nil
}
