//go:build desktop

package shell

import (
	"os"
	"path/filepath"
	"testing"
)

// What may name the folder a new chat opens in. It arrives from outside the
// window - a row of the task bar's menu, written by an earlier launch - and
// the chat's workspace is where the agent reads and writes, so it is held to
// what a person could have picked in the window itself.

func TestAFolderThatIsThereOpensAChat(t *testing.T) {
	dir := t.TempDir()
	got, err := TabFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("the chat would open in %q", got)
	}
	// Nothing named is the window's own default workspace, not a refusal.
	if got, err := TabFolder("  "); err != nil || got != "" {
		t.Fatalf("an unnamed folder answered %q, %v", got, err)
	}
}

func TestOnlyAnExistingFolderOpensAChat(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"relative/path",
		filepath.Join(dir, "not-there"),
		file,
	} {
		if got, err := TabFolder(raw); err == nil {
			t.Fatalf("%q was taken as a workspace and answered %q", raw, got)
		}
	}
}
