//go:build desktop && linux && !android

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The edges of the entry that opens the window at login.

func TestTheAutostartEntryIsWrittenAndTakenAway(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	entry := filepath.Join(config, "autostart", autostartName)

	if autostartEnabled() {
		t.Fatal("a session that was never asked already opens the window at login")
	}
	if err := setAutostart(true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("no entry was written: %v", err)
	}
	text := string(b)
	for _, want := range []string{"[Desktop Entry]", "Type=Application", "Exec=", "Name=Coddy"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the entry has no %q line:\n%s", want, text)
		}
	}
	if !autostartEnabled() {
		t.Fatal("the entry was written but not seen")
	}
	if err := setAutostart(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("the entry is still there: %v", err)
	}
	// Switching it off twice is what a menu does when a switch is thrown back
	// and forth, and it is not an error.
	if err := setAutostart(false); err != nil {
		t.Fatalf("taking an entry away that is already gone failed: %v", err)
	}
}

func TestTheAutostartEntryQuotesAPathWithASpaceInIt(t *testing.T) {
	line := autostartEntry("/opt/Coddy Agent/coddy-desktop")
	if !strings.Contains(line, `Exec="/opt/Coddy Agent/coddy-desktop"`) {
		t.Fatalf("an unquoted Exec line would start something else:\n%s", line)
	}
	if strings.Contains(autostartEntry("/usr/bin/coddy-desktop"), `"`) {
		t.Fatal("an ordinary path is quoted for no reason")
	}
}
