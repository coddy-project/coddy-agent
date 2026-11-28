//go:build desktop && linux && !android

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMenuShortcutPreservesActions(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	path := filepath.Join(data, "applications", "coddy-desktop.desktop")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	old := "[Desktop Entry]\nActions=folder0;\n\n[Desktop Action folder0]\nName=work\nExec=/old/coddy desktop --new-chat --cwd /tmp/work\n"
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if err := InstallMenuShortcut("/opt/my coddy/coddy"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Exec="/opt/my coddy/coddy" desktop`, "Actions=folder0;", "Name=work", `Exec="/opt/my coddy/coddy" desktop --new-chat --cwd /tmp/work`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "icons/hicolor/128x128/apps/coddy-desktop.png")); err != nil {
		t.Fatal(err)
	}
	if err := InstallMenuShortcut("relative"); err == nil {
		t.Fatal("relative executable accepted")
	}
}
