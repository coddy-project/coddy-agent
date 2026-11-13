//go:build desktop && !darwin && !windows && (!linux || android)

package desktop

import (
	"strings"
	"testing"
)

// isolateAutostart has nothing to isolate here: this platform writes no entry.
func isolateAutostart(t *testing.T) { t.Helper() }

// A platform with no autostart Coddy knows says so, rather than reporting a
// switch that took and leaving the menu drawing a lie.
func TestOpeningAtLoginSaysItIsNotWrittenForThisPlatform(t *testing.T) {
	err := setAutostart(true)
	if err == nil {
		t.Fatal("a switch nobody can throw was reported as thrown")
	}
	if !strings.Contains(err.Error(), "autostart") {
		t.Fatalf("the refusal does not say what failed: %v", err)
	}
	if autostartEnabled() {
		t.Fatal("the menu would draw a tick for an entry that is not there")
	}
}
