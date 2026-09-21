//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsoleStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	saveConsoleState(home, consoleState{LastModel: "zed/m"})
	got := loadConsoleState(home)
	if got.LastModel != "zed/m" {
		t.Fatalf("LastModel = %q, want %q", got.LastModel, "zed/m")
	}
}

func TestLoadConsoleStateMissingFile(t *testing.T) {
	got := loadConsoleState(t.TempDir())
	if got.LastModel != "" {
		t.Fatalf("LastModel = %q, want empty for a missing file", got.LastModel)
	}
}

func TestLoadConsoleStateCorruptFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(consoleStatePath(home), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadConsoleState(home)
	if got.LastModel != "" {
		t.Fatalf("LastModel = %q, want empty for a corrupt file", got.LastModel)
	}
}

func TestSaveConsoleStateEmptyHome(t *testing.T) {
	// Must not panic or write anywhere.
	saveConsoleState("", consoleState{LastModel: "x"})
	if _, err := os.Stat(consoleStatePath("")); err == nil {
		t.Fatal("a state file appeared for an empty home")
	}
}

func TestSaveConsoleStatePreservesUnknownFieldsLater(t *testing.T) {
	home := t.TempDir()
	saveConsoleState(home, consoleState{LastModel: "a/m"})
	if _, err := os.Stat(filepath.Join(home, "console-state.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestPickInitialModel(t *testing.T) {
	cases := []struct {
		name    string
		stored  string
		offered []string
		want    string
	}{
		{name: "stored wins", stored: "zed/m", offered: []string{"aaa/m", "zed/m"}, want: "zed/m"},
		{name: "no stored picks alphabetical", stored: "", offered: []string{"zed/m", "aaa/m"}, want: "aaa/m"},
		{name: "stored gone picks alphabetical", stored: "removed/m", offered: []string{"zed/m", "aaa/m"}, want: "aaa/m"},
		{name: "empty offered", stored: "zed/m", offered: nil, want: ""},
		{name: "blank ids skipped", stored: "", offered: []string{"  ", "zed/m"}, want: "zed/m"},
		{name: "stored wins only when offered", stored: "zed/m", offered: []string{"other/m"}, want: "other/m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickInitialModel(tc.stored, tc.offered); got != tc.want {
				t.Fatalf("pickInitialModel(%q, %v) = %q, want %q", tc.stored, tc.offered, got, tc.want)
			}
		})
	}
}
