package llm

import (
	"os"
	"path/filepath"
	"testing"
)

// CodexLoginStored is what a surface asks while it draws: the row's own
// sign-in, else the Codex CLI login when the row may use it, read without
// the lock a refresh holds.
func TestCodexLoginStored(t *testing.T) {
	dir := t.TempDir()
	cliHome := filepath.Join(dir, "codex-cli")
	t.Setenv("CODEX_HOME", cliHome)
	own := filepath.Join(dir, "codex-auth.json")
	signedIn := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"a","refresh_token":"r"}}`)

	if CodexLoginStored(own, true) {
		t.Fatal("no file anywhere is no sign-in")
	}
	if err := os.WriteFile(own, signedIn, 0o600); err != nil {
		t.Fatal(err)
	}
	if !CodexLoginStored(own, false) {
		t.Fatal("the row's own sign-in counts")
	}
	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cliHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliHome, "auth.json"), signedIn, 0o600); err != nil {
		t.Fatal(err)
	}
	if !CodexLoginStored(own, true) {
		t.Fatal("the Codex CLI login stands in for a row that may use it")
	}
	if CodexLoginStored(own, false) {
		t.Fatal("a row the CLI login does not serve cannot borrow it")
	}
	if err := os.Mkdir(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if !CodexLoginStored(own, true) {
		t.Fatal("a directory at the row's path is no file: the CLI login still counts")
	}
}
