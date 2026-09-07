package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func testConfigToolsEnv(t *testing.T, body string) *tooling.Env {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return &tooling.Env{
		SessionID:  filepath.Base(dir),
		SessionDir: dir,
		ConfigPath: path,
		ConfigHome: dir,
		ConfigCWD:  dir,
	}
}

func stageCommands(t *testing.T, env *tooling.Env, commands ...string) string {
	t.Helper()
	args, err := json.Marshal(map[string]interface{}{"commands": commands})
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeConfigSet(context.Background(), string(args), env)
	if err != nil {
		t.Fatalf("config_set(%v): %v", commands, err)
	}
	return out
}

func pendingOf(t *testing.T, env *tooling.Env) []string {
	t.Helper()
	raw, err := executeConfigChanges(context.Background(), "{}", env)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pending []string `json:"pending"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	return got.Pending
}

func TestConfigCommitRollsBackWhenRuntimeReloadFails(t *testing.T) {
	const original = "agent:\n  max_turns: 21\n"
	env := testConfigToolsEnv(t, original)
	reloads := 0
	env.ReloadConfig = func(context.Context) ([]string, error) {
		reloads++
		if reloads == 1 {
			return nil, os.ErrInvalid
		}
		return nil, nil
	}
	stageCommands(t, env, "set agent.max_turns=7")

	_, err := executeConfigCommit(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "change rolled back") {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloads != 2 {
		t.Fatalf("reloads = %d, want failed apply plus restored apply", reloads)
	}
	raw, readErr := os.ReadFile(env.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("config was not rolled back: %q", raw)
	}
	if pending := pendingOf(t, env); len(pending) != 1 {
		t.Fatalf("staged commands must survive a failed commit, got %v", pending)
	}
}

func TestConfigCommitRefusesWithoutRuntimeReload(t *testing.T) {
	env := testConfigToolsEnv(t, "agent: {}\n")
	stageCommands(t, env, "set agent.max_turns=7")
	_, err := executeConfigCommit(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "config was not changed") {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, _ := os.ReadFile(env.ConfigPath)
	if string(raw) != "agent: {}\n" {
		t.Fatalf("config changed without reload hook: %q", raw)
	}
}

func TestConfigCommitRequiresStagedCommands(t *testing.T) {
	env := testConfigToolsEnv(t, "agent: {}\n")
	env.ReloadConfig = func(context.Context) ([]string, error) { return nil, nil }
	_, err := executeConfigCommit(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "no staged config commands") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigSetRejectsInvalidBatchAtomically(t *testing.T) {
	env := testConfigToolsEnv(t, "agent:\n  max_turns: 21\n")
	args := `{"commands":["set agent.max_turns=7","set agent.unknown_field=1"]}`
	_, err := executeConfigSet(context.Background(), args, env)
	if err == nil || !strings.Contains(err.Error(), "unknown config path segment") {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending := pendingOf(t, env); len(pending) != 0 {
		t.Fatalf("a rejected batch must stage nothing, got %v", pending)
	}
}

func TestConfigStagingPersistsAcrossEnvRebuild(t *testing.T) {
	env := testConfigToolsEnv(t, "agent:\n  max_turns: 21\n")
	stageCommands(t, env, "set agent.max_turns=7")

	// A fresh Env with the same SessionDir models the permission-resume path,
	// where the tool call is re-executed after a restart.
	resumed := &tooling.Env{
		SessionID:  env.SessionID,
		SessionDir: env.SessionDir,
		ConfigPath: env.ConfigPath,
		ConfigHome: env.ConfigHome,
		ConfigCWD:  env.ConfigCWD,
	}
	if pending := pendingOf(t, resumed); len(pending) != 1 || pending[0] != "set agent.max_turns=7" {
		t.Fatalf("staged commands lost across env rebuild: %v", pending)
	}

	resumed.ReloadConfig = func(context.Context) ([]string, error) { return nil, nil }
	if _, err := executeConfigCommit(context.Background(), "{}", resumed); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "max_turns: 7") {
		t.Fatalf("resumed commit did not apply: %q", raw)
	}
	if pending := pendingOf(t, env); len(pending) != 0 {
		t.Fatalf("commit must clear staging, got %v", pending)
	}
}

func TestConfigStagingMemoryFallbackWithoutSessionDir(t *testing.T) {
	env := testConfigToolsEnv(t, "agent:\n  max_turns: 21\n")
	env.SessionDir = ""
	env.SessionID = "mem-fallback-session"
	t.Cleanup(func() {
		configStagingMu.Lock()
		delete(configStagingMem, "mem-fallback-session")
		configStagingMu.Unlock()
	})
	stageCommands(t, env, "set agent.max_turns=7")
	other := &tooling.Env{
		SessionID:  env.SessionID,
		ConfigPath: env.ConfigPath,
		ConfigHome: env.ConfigHome,
		ConfigCWD:  env.ConfigCWD,
	}
	if pending := pendingOf(t, other); len(pending) != 1 {
		t.Fatalf("memory staging lost for the same session id: %v", pending)
	}
}

func TestConfigRevertDropsAllOrByPath(t *testing.T) {
	env := testConfigToolsEnv(t, "agent:\n  max_turns: 21\nskills:\n  dirs: []\n")
	stageCommands(t, env, "set agent.max_turns=7", "add_list skills.dirs=/opt/skills")

	out, err := executeConfigRevert(context.Background(), `{"path":"agent.max_turns"}`, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "add_list skills.dirs=/opt/skills") || strings.Contains(out, "max_turns") {
		t.Fatalf("path revert kept the wrong commands: %s", out)
	}

	if _, err := executeConfigRevert(context.Background(), "{}", env); err != nil {
		t.Fatal(err)
	}
	if pending := pendingOf(t, env); len(pending) != 0 {
		t.Fatalf("full revert left commands staged: %v", pending)
	}
}

func TestConfigCommitAbortsWhenStagingCannotBeConsumed(t *testing.T) {
	const original = "agent:\n  max_turns: 21\n"
	env := testConfigToolsEnv(t, original)
	reloads := 0
	env.ReloadConfig = func(context.Context) ([]string, error) {
		reloads++
		return nil, nil
	}
	stageCommands(t, env, "set agent.max_turns=7")

	// A read-only session dir makes removing config_staging.json fail while
	// reads still work. The commit must then abort before touching the config,
	// so a later retry cannot replay an already applied batch.
	if err := os.Chmod(env.SessionDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(env.SessionDir, 0o755) })

	_, err := executeConfigCommit(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "config was not changed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloads != 0 {
		t.Fatalf("reloads = %d, want 0", reloads)
	}
	raw, readErr := os.ReadFile(env.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("aborted commit changed config: %q", raw)
	}
	if pending := pendingOf(t, env); len(pending) != 1 {
		t.Fatalf("staged commands must survive the aborted commit, got %v", pending)
	}
}

func TestConfigCommitKeepsStagingConsumedWhenRollbackFails(t *testing.T) {
	const original = "agent:\n  max_turns: 21\nskills:\n  dirs:\n    - /opt/base\n"
	env := testConfigToolsEnv(t, original)
	dir := filepath.Dir(env.ConfigPath)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	reloads := 0
	env.ReloadConfig = func(context.Context) ([]string, error) {
		reloads++
		if reloads == 1 {
			// Sabotage the directory before failing the reload, so the tool's
			// commit.Rollback() cannot restore the previous file: the committed
			// change stays active on disk.
			if err := os.Chmod(dir, 0o555); err != nil {
				return nil, err
			}
			return nil, os.ErrInvalid
		}
		return nil, nil
	}
	stageCommands(t, env, "add_list skills.dirs=/opt/extra")

	_, err := executeConfigCommit(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "stay consumed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The non-idempotent command is live in the file, so it must not be
	// replayable from staging.
	raw, readErr := os.ReadFile(env.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), "/opt/extra") {
		t.Fatalf("expected the committed change to remain active: %q", raw)
	}
	if pending := pendingOf(t, env); len(pending) != 0 {
		t.Fatalf("staged commands must stay consumed after a failed rollback, got %v", pending)
	}
}

func TestConfigRevertWithPathOnEmptyStagingSucceeds(t *testing.T) {
	env := testConfigToolsEnv(t, "agent: {}\n")
	out, err := executeConfigRevert(context.Background(), `{"path":"agent.max_turns"}`, env)
	if err != nil {
		t.Fatalf("revert with path on empty staging: %v", err)
	}
	if !strings.Contains(out, `"pending":[]`) {
		t.Fatalf("unexpected revert result: %s", out)
	}
}

func TestConfigToolOutputsRedactSecretValues(t *testing.T) {
	env := testConfigToolsEnv(t, "agent: {}\n")
	out := stageCommands(t, env, "set httpserver.auth_token=t0p-secret")
	if strings.Contains(out, "t0p-secret") {
		t.Fatalf("config_set echoed a secret value: %s", out)
	}
	listed, err := executeConfigChanges(context.Background(), "{}", env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed, "t0p-secret") || !strings.Contains(listed, "<redacted>") {
		t.Fatalf("config_changes leaked a secret value: %s", listed)
	}
	if summary := PendingConfigSummary(env); len(summary) != 1 || strings.Contains(summary[0], "t0p-secret") {
		t.Fatalf("permission summary leaked a secret value: %v", summary)
	}
	// The staging store must keep the original value for the commit to apply.
	configStagingMu.Lock()
	stored, err := loadStagedConfigCommands(env)
	configStagingMu.Unlock()
	if err != nil || len(stored) != 1 || !strings.Contains(stored[0], "t0p-secret") {
		t.Fatalf("staging must keep the raw command: %v, %v", stored, err)
	}

	// The commit result (its "applied" list goes into the transcript) must be
	// masked too, while the file receives the raw value.
	env.ReloadConfig = func(context.Context) ([]string, error) { return nil, nil }
	committed, err := executeConfigCommit(context.Background(), "{}", env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(committed, "t0p-secret") || !strings.Contains(committed, "<redacted>") {
		t.Fatalf("config_commit leaked a secret value: %s", committed)
	}
	raw, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "t0p-secret") {
		t.Fatalf("commit must write the raw value to disk: %q", raw)
	}
}

func TestConfigRollbackWithoutSnapshotErrors(t *testing.T) {
	env := testConfigToolsEnv(t, "agent:\n  max_turns: 21\n")
	env.ReloadConfig = func(context.Context) ([]string, error) { return nil, nil }
	_, err := executeConfigRollback(context.Background(), "{}", env)
	if err == nil || !strings.Contains(err.Error(), "nothing to roll back to") {
		t.Fatalf("unexpected error: %v", err)
	}
}
