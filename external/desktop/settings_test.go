//go:build desktop

package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The edges of the switches in the menu: what the file keeps, and what happens
// to a switch this machine cannot actually throw.

func TestTheSwitchesComeBackFromTheFile(t *testing.T) {
	home := t.TempDir()
	store := LoadSettings(home, nil)
	if err := store.SetSettings(Settings{StartMinimised: true}); err != nil {
		t.Fatal(err)
	}
	again := LoadSettings(home, nil)
	if !again.Settings().StartMinimised {
		t.Fatal("the next launch did not open minimised, though the switch was on")
	}
	b, err := os.ReadFile(filepath.Join(home, "desktop", settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(b, &written); err != nil {
		t.Fatalf("the settings file is not readable JSON: %v", err)
	}
	if written["start_minimised"] != true {
		t.Fatalf("the file says %v", written)
	}
}

// The same switches are drawn in two places - the menu in the bar and the
// menu of the tray icon - and a switch thrown in one has to move in the other.
// Neither menu watches the other: the store announces the change, and the
// launcher's watcher is what redraws the tray and tells the page to read again.
func TestAChangeIsAnnouncedToWhoeverDrawsIt(t *testing.T) {
	store := LoadSettings(t.TempDir(), nil)
	told := 0
	store.watch(func() { told++ })
	if err := store.SetSettings(Settings{StartMinimised: true}); err != nil {
		t.Fatal(err)
	}
	if told != 1 {
		t.Fatalf("the change was announced %d times", told)
	}
	// A write that changes nothing is still a write, and announcing it costs a
	// redraw of a menu that is almost never open; a refusal announces nothing,
	// because nothing moved.
	store.tray = nil
	if err := store.SetSettings(Settings{StartMinimised: true, Tray: true}); err == nil {
		t.Fatal("a tray icon nobody can show was reported as shown")
	}
	if told != 1 {
		t.Fatalf("a refused change was announced: %d", told)
	}
}

func TestADefaultWindowOpensWhenThereIsNoFileYet(t *testing.T) {
	if got := LoadSettings(t.TempDir(), nil).Settings(); got != (Settings{}) {
		t.Fatalf("a home with no settings file opened on %+v", got)
	}
}

func TestASwitchThatCouldNotBeThrownIsNotWrittenDown(t *testing.T) {
	home := t.TempDir()
	// No tray was bound to this store, which is a machine with no tray to put
	// an icon in.
	store := LoadSettings(home, nil)
	err := store.SetSettings(Settings{Tray: true})
	if err == nil {
		t.Fatal("a tray icon nobody can show was reported as shown")
	}
	if !strings.Contains(err.Error(), "tray") {
		t.Fatalf("the refusal does not say what failed: %v", err)
	}
	if store.Settings().Tray {
		t.Fatal("the menu would draw a tick for a tray icon that is not there")
	}
	if _, err := os.Stat(filepath.Join(home, "desktop", settingsFile)); err == nil {
		t.Fatal("a switch that did not take was written down anyway")
	}
}

func TestASettingsFileNobodyCanReadStillOpensAWindow(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "desktop"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "desktop", settingsFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(home, nil).Settings(); got != (Settings{}) {
		t.Fatalf("a settings file that cannot be read opened on %+v, wanted the defaults", got)
	}
}
