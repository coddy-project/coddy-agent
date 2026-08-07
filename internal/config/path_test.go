package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPathConfig(t *testing.T, body string) Paths {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Paths{Home: dir, CWD: dir, ConfigPath: path}
}

func TestEditConfigPathSelectorSetAppendAndDelete(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 19\nmcp_servers: []\n")
	value := json.RawMessage(`{"command":"npx","args":["-y","@upstash/context7-mcp"]}`)
	result, err := EditConfigPath(paths, "/mcp_servers[name=context7]", value, false)
	if err != nil {
		t.Fatalf("EditConfigPath set: %v", err)
	}
	if !result.Changed || result.Config.Agent.MaxTurns != 19 {
		t.Fatalf("unrelated config was not preserved: %+v", result.Config.Agent)
	}
	if len(result.Config.MCPServers) != 1 || result.Config.MCPServers[0].Name != "context7" {
		t.Fatalf("selector did not append named MCP: %+v", result.Config.MCPServers)
	}

	got, err := ReadConfigPath(paths, "/mcp_servers[name=context7]/args/1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Value != "@upstash/context7-mcp" {
		t.Fatalf("ReadConfigPath = %#v", got)
	}

	if _, err := EditConfigPath(paths, "/mcp_servers[name=context7]", nil, true); err != nil {
		t.Fatalf("EditConfigPath delete: %v", err)
	}
	reloaded, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.MCPServers) != 0 {
		t.Fatalf("MCP selector was not deleted: %+v", reloaded.MCPServers)
	}
}

func TestReadConfigPathRedactsSecrets(t *testing.T) {
	paths := testPathConfig(t, `providers:
  - name: openai
    type: openai
    api_key: sk-secret
mcp_servers:
  - name: github
    command: npx
    env:
      - name: GITHUB_TOKEN
        value: gh-secret
httpserver:
  auth_token: http-secret
`)
	got, err := ReadConfigPath(paths, "/")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got.Value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"sk-secret", "gh-secret", "http-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("config_get leaked %q in %s", secret, text)
		}
	}
	if !got.Redacted || strings.Count(text, "redacted") != 3 {
		t.Fatalf("redaction metadata/value missing: %+v %s", got, text)
	}
}

func TestEditConfigPathRejectsUnknownSchemaPathWithoutWriting(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	_, err := EditConfigPath(paths, "/mcp_servers[name=demo]/unknown", json.RawMessage(`true`), false)
	if err == nil || !strings.Contains(err.Error(), "unknown config path segment") {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, readErr := os.ReadFile(paths.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("invalid edit changed config: %q", raw)
	}
}

func TestConfigEditRollbackRestoresMissingFile(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	result, err := EditConfigPath(paths, "/agent/max_turns", json.RawMessage(`9`), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("rollback should remove newly created config, stat err = %v", err)
	}
	if _, err := os.Stat(BackupPath(paths.ConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("rollback should remove newly created backup, stat err = %v", err)
	}
}

func TestConfigEditRollbackRestoresPreviousBackup(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 13\n")
	const previousBackup = "agent:\n  max_turns: 12\n"
	if err := os.WriteFile(BackupPath(paths.ConfigPath), []byte(previousBackup), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := EditConfigPath(paths, "/agent/max_turns", json.RawMessage(`9`), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Rollback(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(BackupPath(paths.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != previousBackup {
		t.Fatalf("backup after rollback = %q", backup)
	}
}

func TestDeleteMissingSelectorDoesNotCreateSequence(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	result, err := EditConfigPath(paths, "/mcp_servers[name=missing]", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("deleting a missing selector should be a no-op")
	}
	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("delete created an empty sequence: %q", raw)
	}
}
