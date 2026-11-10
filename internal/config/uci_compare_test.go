package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitUCICommandsIfUnchanged(t *testing.T) {
	home := t.TempDir()
	paths := Paths{Home: home, ConfigPath: filepath.Join(home, "config.yaml")}
	original := []byte("agent:\n  max_turns: 11\nlogger:\n  level: info\n")
	if err := os.WriteFile(paths.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	command := []UCICommand{{Op: UCIOpSet, Path: "agent.max_turns", Value: "21"}}
	if _, err := CommitUCICommandsIfUnchanged(paths, []byte("stale"), command); err == nil {
		t.Fatal("stale config was accepted")
	}
	unchanged, err := os.ReadFile(paths.ConfigPath)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatalf("conflict changed config: %v", err)
	}
	result, err := CommitUCICommandsIfUnchanged(paths, original, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config == nil || result.Config.Agent.MaxTurns != 21 {
		t.Fatal("requested change was not committed")
	}
	updated, err := os.ReadFile(paths.ConfigPath)
	if err != nil || !bytes.Contains(updated, []byte("level: info")) {
		t.Fatalf("unrelated setting lost: %v", err)
	}
}

func TestCommitUCICommandsIfUnchangedDistinguishesMissingAndEmpty(t *testing.T) {
	paths := Paths{Home: t.TempDir()}
	paths.ConfigPath = filepath.Join(paths.Home, "config.yaml")
	commands := []UCICommand{{Op: UCIOpSet, Path: "agent.max_turns", Value: "20"}}
	if err := os.WriteFile(paths.ConfigPath, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitUCICommandsIfUnchanged(paths, nil, commands); err == nil {
		t.Fatal("accepted a file created after the missing snapshot")
	}
	if err := os.Remove(paths.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitUCICommandsIfUnchanged(paths, []byte{}, commands); err == nil {
		t.Fatal("accepted deletion after the empty snapshot")
	}
}
