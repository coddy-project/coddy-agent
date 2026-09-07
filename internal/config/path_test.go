package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func mustParseUCI(t *testing.T, lines ...string) []UCICommand {
	t.Helper()
	cmds, err := ParseUCICommands(lines)
	if err != nil {
		t.Fatalf("ParseUCICommands(%v): %v", lines, err)
	}
	return cmds
}

func TestParseUCICommandGrammar(t *testing.T) {
	cases := []struct {
		line    string
		want    UCICommand
		wantErr string
	}{
		{line: "set agent.max_turns=20", want: UCICommand{Op: "set", Path: "agent.max_turns", Value: "20"}},
		{line: "set logger.level='debug'", want: UCICommand{Op: "set", Path: "logger.level", Value: "debug"}},
		{line: "add_list skills.dirs=/opt/skills", want: UCICommand{Op: "add_list", Path: "skills.dirs", Value: "/opt/skills"}},
		{line: "del_list skills.dirs=/opt/skills", want: UCICommand{Op: "del_list", Path: "skills.dirs", Value: "/opt/skills"}},
		{line: "delete mcp_servers[name=old]", want: UCICommand{Op: "delete", Path: "mcp_servers[name=old]"}},
		{line: "set mcp_servers[name=a.b].command=npx", want: UCICommand{Op: "set", Path: "mcp_servers[name=a.b].command", Value: "npx"}},
		{line: "rename agent.max_turns=x", wantErr: "not supported"},
		{line: "set agent.max_turns", wantErr: "expected"},
		{line: "set =7", wantErr: "path before"},
		{line: "delete mcp_servers[name=]", wantErr: "invalid selector"},
		{line: "set", wantErr: "must be"},
	}
	for _, tc := range cases {
		got, err := ParseUCICommand(tc.line)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseUCICommand(%q) err = %v, want containing %q", tc.line, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseUCICommand(%q): %v", tc.line, err)
		}
		if got != tc.want {
			t.Fatalf("ParseUCICommand(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
	}
}

func TestCommitUCICommandsSelectorSetAppendAndDelete(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 19\nmcp_servers: []\n")
	cmds := mustParseUCI(t, `set mcp_servers[name=context7]={"command":"npx","args":["-y","@upstash/context7-mcp"]}`)
	result, err := CommitUCICommands(paths, cmds)
	if err != nil {
		t.Fatalf("CommitUCICommands set: %v", err)
	}
	if !result.Changed || result.Config.Agent.MaxTurns != 19 {
		t.Fatalf("unrelated config was not preserved: %+v", result.Config.Agent)
	}
	if len(result.Config.MCPServers) != 1 || result.Config.MCPServers[0].Name != "context7" {
		t.Fatalf("selector did not append named MCP: %+v", result.Config.MCPServers)
	}

	got, err := ReadConfigPath(paths, "mcp_servers[name=context7].args.1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Value != "@upstash/context7-mcp" {
		t.Fatalf("ReadConfigPath = %#v", got)
	}

	snapshot, err := os.ReadFile(PrevConfigPath(paths.ConfigPath))
	if err != nil {
		t.Fatalf("pre-commit snapshot missing: %v", err)
	}
	if !strings.Contains(string(snapshot), "max_turns: 19") || strings.Contains(string(snapshot), "context7") {
		t.Fatalf("snapshot does not hold the previous config: %q", snapshot)
	}

	if _, err := CommitUCICommands(paths, mustParseUCI(t, "delete mcp_servers[name=context7]")); err != nil {
		t.Fatalf("CommitUCICommands delete: %v", err)
	}
	reloaded, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.MCPServers) != 0 {
		t.Fatalf("MCP selector was not deleted: %+v", reloaded.MCPServers)
	}
}

func TestCommitUCICommandsListOps(t *testing.T) {
	paths := testPathConfig(t, "skills:\n  dirs:\n    - /opt/a\n")
	if _, err := CommitUCICommands(paths, mustParseUCI(t, "add_list skills.dirs=/opt/b")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfigPath(paths, "skills.dirs.1")
	if err != nil || !got.Exists || got.Value != "/opt/b" {
		t.Fatalf("append failed: %#v, %v", got, err)
	}

	if _, err := CommitUCICommands(paths, mustParseUCI(t, "del_list skills.dirs=/opt/a")); err != nil {
		t.Fatal(err)
	}
	got, err = ReadConfigPath(paths, "skills.dirs.0")
	if err != nil || !got.Exists || got.Value != "/opt/b" {
		t.Fatalf("del_list did not remove the entry: %#v, %v", got, err)
	}

	_, err = CommitUCICommands(paths, mustParseUCI(t, "del_list skills.dirs=/missing"))
	if err == nil || !strings.Contains(err.Error(), "no list entry equals") {
		t.Fatalf("del_list of a missing value should fail, got %v", err)
	}
}

func TestCommitUCICommandsStringFieldKeepsLiteralText(t *testing.T) {
	paths := testPathConfig(t, "logger:\n  level: info\n")
	if _, err := CommitUCICommands(paths, mustParseUCI(t, "set logger.level=debug")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logger.Level != "debug" {
		t.Fatalf("logger.level = %q", cfg.Logger.Level)
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
	for _, root := range []string{"/", "."} {
		got, err := ReadConfigPath(paths, root)
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
}

func TestCommitRejectsUnknownSchemaPathWithoutWriting(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	_, err := CommitUCICommands(paths, mustParseUCI(t, "set mcp_servers[name=demo].unknown=true"))
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
	if _, err := os.Stat(PrevConfigPath(paths.ConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("failed commit left a snapshot, stat err = %v", err)
	}
}

func TestUCICommandRedactedString(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{line: "set providers.0.api_key=sk-secret", want: "set providers.0.api_key=<redacted>"},
		{line: "set providers.0.api_key_command=vault read key", want: "set providers.0.api_key_command=<redacted>"},
		{line: "set mcp_servers[name=x].env.0.value=tok-123", want: "set mcp_servers[name=x].env.0.value=<redacted>"},
		{line: "set httpserver.auth_token='t0p'", want: "set httpserver.auth_token=<redacted>"},
		{line: "add_list skills.dirs=/opt/skills", want: "add_list skills.dirs=/opt/skills"},
		{line: "delete mcp_servers[name=x]", want: "delete mcp_servers[name=x]"},
		{line: "set agent.max_turns=20", want: "set agent.max_turns=20"},
	}
	for _, tc := range cases {
		cmd, err := ParseUCICommand(tc.line)
		if err != nil {
			t.Fatalf("ParseUCICommand(%q): %v", tc.line, err)
		}
		if got := cmd.RedactedString(); got != tc.want {
			t.Fatalf("RedactedString(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}

	// Secrets embedded in a JSON object value are masked field by field.
	cmd, err := ParseUCICommand(`set mcp_servers[name=x]={"command":"npx","env":[{"name":"K","value":"tok-embedded"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	got := cmd.RedactedString()
	if strings.Contains(got, "tok-embedded") || !strings.Contains(got, "<redacted>") || !strings.Contains(got, "npx") {
		t.Fatalf("embedded secret not masked: %q", got)
	}
}

func TestFirstCommitIntoMissingFileWritesSnapshotForRollback(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	commit, err := CommitUCICommands(paths, mustParseUCI(t, "set agent.max_turns=9"))
	if err != nil {
		t.Fatal(err)
	}
	if commit.Config.Agent.MaxTurns != 9 {
		t.Fatalf("first commit did not apply: %+v", commit.Config.Agent)
	}
	snapshot, err := os.ReadFile(PrevConfigPath(paths.ConfigPath))
	if err != nil {
		t.Fatalf("first commit must leave a snapshot: %v", err)
	}
	if len(snapshot) != 0 {
		t.Fatalf("snapshot of a missing file should be empty, got %q", snapshot)
	}

	rollback, err := RollbackConfigFromSnapshot(paths)
	if err != nil {
		t.Fatalf("rollback after first commit: %v", err)
	}
	if rollback.Config.Agent.MaxTurns == 9 {
		t.Fatal("rollback did not return to the pre-commit defaults")
	}
	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("restored config should be the empty pre-commit document, got %q", raw)
	}
}

func TestCommitRollbackRestoresMissingFile(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{Home: dir, CWD: dir, ConfigPath: filepath.Join(dir, "config.yaml")}
	result, err := CommitUCICommands(paths, mustParseUCI(t, "set agent.max_turns=9"))
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("rollback should remove newly created config, stat err = %v", err)
	}
	if _, err := os.Stat(PrevConfigPath(paths.ConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("rollback should not leave a snapshot, stat err = %v", err)
	}
}

func TestDeleteMissingSelectorErrorsWithoutWriting(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	_, err := CommitUCICommands(paths, mustParseUCI(t, "delete mcp_servers[name=missing]"))
	if err == nil || !strings.Contains(err.Error(), "path does not exist") {
		t.Fatalf("deleting a missing selector should error, got %v", err)
	}
	raw, readErr := os.ReadFile(paths.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("failed delete changed config: %q", raw)
	}
}

func TestRollbackConfigFromSnapshotSwaps(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	if _, err := CommitUCICommands(paths, mustParseUCI(t, "set agent.max_turns=20")); err != nil {
		t.Fatal(err)
	}

	rollback, err := RollbackConfigFromSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Config.Agent.MaxTurns != 13 {
		t.Fatalf("rollback restored max_turns = %d, want 13", rollback.Config.Agent.MaxTurns)
	}
	// The replaced config swaps into the snapshot slot, so a second rollback
	// brings the committed change back.
	again, err := RollbackConfigFromSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	if again.Config.Agent.MaxTurns != 20 {
		t.Fatalf("second rollback restored max_turns = %d, want 20", again.Config.Agent.MaxTurns)
	}
}

// The transaction functions roll their files back on internal failures while
// still holding configPathWriteMu, so the helper they use must never try to
// take that non-reentrant mutex again. Calling it under a held lock would
// deadlock (and time the test run out) if the helper regressed to locking.
func TestInternalRollbackHelpersRunUnderHeldWriteLock(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 13\n")
	commit, err := CommitUCICommands(paths, mustParseUCI(t, "set agent.max_turns=20"))
	if err != nil {
		t.Fatal(err)
	}
	configPathWriteMu.Lock()
	err = commit.rollbackLocked()
	configPathWriteMu.Unlock()
	if err != nil {
		t.Fatalf("commit rollbackLocked: %v", err)
	}
	if got := maxTurnsOf(t, paths); got != 13 {
		t.Fatalf("commit rollback restored max_turns = %d, want 13", got)
	}

	if _, err := CommitUCICommands(paths, mustParseUCI(t, "set agent.max_turns=20")); err != nil {
		t.Fatal(err)
	}
	rollback, err := RollbackConfigFromSnapshot(paths)
	if err != nil {
		t.Fatal(err)
	}
	configPathWriteMu.Lock()
	err = rollback.rollbackLocked()
	configPathWriteMu.Unlock()
	if err != nil {
		t.Fatalf("snapshot rollbackLocked: %v", err)
	}
	if got := maxTurnsOf(t, paths); got != 20 {
		t.Fatalf("snapshot rollback undo restored max_turns = %d, want 20", got)
	}
}

func maxTurnsOf(t *testing.T, paths Paths) int {
	t.Helper()
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Agent.MaxTurns
}

// The whole transaction - disk mutation plus runtime application - must run
// under one lock. If a second writer could squeeze in between another
// transaction's file write and its runtime install, the process could end up
// running a config that no longer matches the file.
func TestConfigWritersSerializeFileAndRuntimeApplication(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 1\n")

	installed := ""
	install := func() {
		raw, err := os.ReadFile(paths.ConfigPath)
		if err != nil {
			t.Errorf("read config during install: %v", err)
			return
		}
		installed = string(raw)
	}

	cmds := mustParseUCI(t, "set agent.max_turns=2")
	inReload := make(chan struct{})
	release := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		_, _, err := CommitUCICommandsAndReload(paths, cmds, func() ([]string, error) {
			close(inReload)
			<-release
			install()
			return nil, nil
		})
		commitDone <- err
	}()
	// The commit transaction wrote the file and is paused mid-reload, still
	// holding the config file lock.
	<-inReload

	putDone := make(chan error, 1)
	go func() {
		putDone <- WithConfigFileLock(func() error {
			if err := AtomicWriteConfigYAML(paths.ConfigPath, []byte("agent:\n  max_turns: 3\n")); err != nil {
				return err
			}
			install()
			return nil
		})
	}()
	select {
	case <-putDone:
		t.Fatal("second writer entered while the commit transaction still held the lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if installed != string(final) {
		t.Fatalf("runtime installed %q but the file holds %q", installed, final)
	}
	if !strings.Contains(installed, "max_turns: 3") {
		t.Fatalf("final installed config should be the second writer's: %q", installed)
	}
}

func TestRollbackConfigWithoutSnapshotErrors(t *testing.T) {
	paths := testPathConfig(t, "agent:\n  max_turns: 13\n")
	_, err := RollbackConfigFromSnapshot(paths)
	if err == nil || !strings.Contains(err.Error(), "nothing to roll back to") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDryRunUCICommandsLeavesFileUntouched(t *testing.T) {
	const original = "agent:\n  max_turns: 13\n"
	paths := testPathConfig(t, original)
	if err := DryRunUCICommands(paths, mustParseUCI(t, "set agent.max_turns=20")); err != nil {
		t.Fatal(err)
	}
	if err := DryRunUCICommands(paths, mustParseUCI(t, "set agent.max_turns=not-a-number")); err == nil {
		t.Fatal("invalid value should fail the dry run")
	}
	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("dry run changed config: %q", raw)
	}
}
