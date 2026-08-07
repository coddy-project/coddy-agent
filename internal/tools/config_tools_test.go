package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestConfigSetRollsBackWhenRuntimeReloadFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const original = "agent:\n  max_turns: 21\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	env := &tooling.Env{ConfigPath: path, ConfigHome: dir, ConfigCWD: dir}
	env.ReloadConfig = func(context.Context) ([]string, error) {
		reloads++
		if reloads == 1 {
			return nil, os.ErrInvalid
		}
		return nil, nil
	}

	_, err := executeConfigSet(context.Background(), `{"path":"/agent/max_turns","value":7}`, env)
	if err == nil || !strings.Contains(err.Error(), "change rolled back") {
		t.Fatalf("unexpected error: %v", err)
	}
	if reloads != 2 {
		t.Fatalf("reloads = %d, want failed apply plus restored apply", reloads)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != original {
		t.Fatalf("config was not rolled back: %q", raw)
	}
}

func TestConfigSetRefusesWriteWithoutRuntimeReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("agent: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := executeConfigSet(context.Background(), `{"path":"/agent/max_turns","value":7}`, &tooling.Env{
		ConfigPath: path,
		ConfigHome: dir,
		ConfigCWD:  dir,
	})
	if err == nil || !strings.Contains(err.Error(), "config was not changed") {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "agent: {}\n" {
		t.Fatalf("config changed without reload hook: %q", raw)
	}
}
