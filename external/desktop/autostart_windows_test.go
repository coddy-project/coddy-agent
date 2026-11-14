//go:build desktop && windows

package desktop

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// The Run value that opens the window at login, on the machine it is written
// for: a cross-compile proves it builds, and nothing more.

// isolateAutostart points the entry at a key of this test's own, so a suite
// neither reads nor writes the Run key of the person running it.
func isolateAutostart(t *testing.T) {
	t.Helper()
	was := autostartRunKey
	autostartRunKey = `Software\Coddy\TestRun`
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, autostartRunKey)
		autostartRunKey = was
	})
}

func TestTheAutostartValueIsWrittenAndTakenAway(t *testing.T) {
	isolateAutostart(t)
	if autostartEnabled() {
		t.Fatal("an account that was never asked already opens the window at login")
	}
	if err := setAutostart(true); err != nil {
		t.Fatal(err)
	}
	if !autostartEnabled() {
		t.Fatal("the value was written but not seen")
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := key.GetStringValue(autostartValue)
	_ = key.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(value, `" desktop`) || !strings.HasPrefix(value, `"`) {
		t.Fatalf("the value is %q: a path with a space in it would start the wrong program", value)
	}
	if err := setAutostart(false); err != nil {
		t.Fatal(err)
	}
	if autostartEnabled() {
		t.Fatal("the value is still there")
	}
	// Twice is what a menu does when a switch is thrown back and forth.
	if err := setAutostart(false); err != nil {
		t.Fatalf("taking a value away that is already gone failed: %v", err)
	}
}

func TestTheAutostartCommandQuotesThePathAndOpensAWindow(t *testing.T) {
	got := autostartCommand(`C:\Program Files\Coddy\coddy.exe`)
	if got != `"C:\Program Files\Coddy\coddy.exe" desktop` {
		t.Fatalf("the command is %q", got)
	}
}
