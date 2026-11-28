//go:build desktop && ((linux && !android) || windows)

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
)

// ShortcutReceiptPath identifies the per-user registration receipt shared by
// the installer and desktop refresh, independently of the agent home.
func ShortcutReceiptPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("user configuration directory is not absolute: %q", dir)
	}
	return filepath.Join(dir, "coddy", "desktop-shortcut-v1"), nil
}
