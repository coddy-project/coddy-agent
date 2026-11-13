//go:build desktop && darwin

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launch agent that opens the window at login, on the machine it is
// written for: a cross-compile proves it builds, and nothing more.

// isolateAutostart points the launch agent at a home of this test's own, so a
// suite neither reads nor writes the LaunchAgents of the person running it.
func isolateAutostart(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestTheLaunchAgentIsWrittenAndTakenAway(t *testing.T) {
	isolateAutostart(t)
	path, err := autostartPath()
	if err != nil {
		t.Fatal(err)
	}
	if autostartEnabled() {
		t.Fatal("a session that was never asked already opens the window at login")
	}
	if err := setAutostart(true); err != nil {
		t.Fatal(err)
	}
	if !autostartEnabled() {
		t.Fatal("the agent was written but not seen")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no agent was written: %v", err)
	}
	text := string(b)
	for _, want := range []string{"<plist", autostartLabel, "<key>RunAtLoad</key>", "<string>desktop</string>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the agent has no %q in it:\n%s", want, text)
		}
	}
	if filepath.Base(path) != autostartLabel+".plist" {
		t.Fatalf("the agent is called %q", filepath.Base(path))
	}
	if err := setAutostart(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the agent is still there: %v", err)
	}
	// Twice is what a menu does when a switch is thrown back and forth.
	if err := setAutostart(false); err != nil {
		t.Fatalf("taking an agent away that is already gone failed: %v", err)
	}
}

// The verb is what makes it a window: the same binary with no argument is the
// interactive console, which is not what a login should open. And a path with
// an ampersand in it is still a path, which XML has an opinion about.
func TestTheLaunchAgentOpensAWindowAndEscapesItsPath(t *testing.T) {
	agent := autostartAgent("/Applications/Coddy & Co/coddy")
	if !strings.Contains(agent, "<string>/Applications/Coddy &amp; Co/coddy</string>") {
		t.Fatalf("the path is not escaped:\n%s", agent)
	}
	if !strings.Contains(agent, "<string>desktop</string>") {
		t.Fatalf("the agent does not open a window:\n%s", agent)
	}
}
