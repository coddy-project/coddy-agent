package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The config, its backup and mcp.json carry credentials (provider keys, bot
// tokens, server tokens, MCP headers), so every write leaves them readable by
// their owner only, whatever mode the file had before.

func requirePOSIXModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits")
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// worldReadable writes body at path and makes it 0644 whatever the umask says.
func worldReadable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigWritesAreOwnerOnly(t *testing.T) {
	requirePOSIXModes(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	worldReadable(t, cfgPath, "agent:\n  max_turns: 3\n")

	if err := AtomicWriteConfigYAML(cfgPath, []byte("agent:\n  max_turns: 4\n")); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, cfgPath); got != 0o600 {
		t.Errorf("config.yaml mode after a save = %o, want 600", got)
	}

	worldReadable(t, BackupPath(cfgPath), "old\n")
	if err := WriteBackup(cfgPath, []byte("agent:\n  max_turns: 4\n")); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, BackupPath(cfgPath)); got != 0o600 {
		t.Errorf("config.yaml.bak mode after WriteBackup = %o, want 600", got)
	}

	if err := os.Remove(BackupPath(cfgPath)); err != nil {
		t.Fatal(err)
	}
	if err := BackupCurrent(cfgPath); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, BackupPath(cfgPath)); got != 0o600 {
		t.Errorf("config.yaml.bak mode after BackupCurrent = %o, want 600", got)
	}
}

func TestLoadWritesTheBackupOwnerOnly(t *testing.T) {
	requirePOSIXModes(t)
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	worldReadable(t, cfgPath, withModeline("agent:\n  max_turns: 3\n"))
	worldReadable(t, BackupPath(cfgPath), "agent:\n  max_turns: 2\n")

	if _, err := LoadFromCLI(CLIPaths{Home: home, Config: cfgPath}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := fileMode(t, BackupPath(cfgPath)); got != 0o600 {
		t.Errorf("config.yaml.bak mode after a load = %o, want 600", got)
	}
}

func TestMCPJSONWritesAreOwnerOnly(t *testing.T) {
	requirePOSIXModes(t)
	path := MCPJSONPath(t.TempDir())
	if err := UpsertMCPJSONServer(path, "demo", MCPJSONServer{Command: "demo-mcp"}); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("new mcp.json mode = %o, want 600", got)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertMCPJSONServer(path, "other", MCPJSONServer{Command: "other-mcp"}); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, path); got != 0o600 {
		t.Errorf("mcp.json mode after a rewrite = %o, want 600", got)
	}
}

func TestCheckWarnsWhenOthersCanReadTheConfig(t *testing.T) {
	requirePOSIXModes(t)
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	worldReadable(t, path, withModeline("agent:\n  max_turns: 3\n"))

	rep, err := Check(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	var found *Finding
	for i, f := range warningsOf(rep) {
		if strings.Contains(f.Message, "other users") {
			found = &warningsOf(rep)[i]
		}
	}
	if found == nil {
		t.Fatalf("no warning about a world-readable config: %+v", rep.Findings)
	}
	if !strings.Contains(found.Fix, "chmod 600 "+path) {
		t.Errorf("fix = %q, want it to name chmod 600 %s", found.Fix, path)
	}
	if !rep.Valid() {
		t.Errorf("a readable config is a warning, not an error: %+v", rep.Findings)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err = Check(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "other users") {
			t.Errorf("owner-only config still warned: %+v", f)
		}
	}
}
