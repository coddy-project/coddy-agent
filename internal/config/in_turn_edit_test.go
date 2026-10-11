package config

import (
	"os"
	"strings"
	"testing"
)

// The agent stages compaction.in_turn.* through config_set, which edits the YAML
// tree: a switch turned off has to reach the file, a count below one is refused
// before anything is written.
func TestCommitUCICommandsInTurnKeysReachTheFileAndRefuseZero(t *testing.T) {
	paths := testPathConfig(t, "compaction:\n  threshold_percent: 70\n")

	if _, err := CommitUCICommands(paths, mustParseUCI(t, "set compaction.in_turn.enable=false", "set compaction.in_turn.keep_recent_steps=2")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compaction.InTurn.IsEnabled() || cfg.Compaction.InTurn.EffectiveKeepRecentSteps() != 2 {
		t.Fatalf("in_turn = %+v, want disabled with 2 steps", cfg.Compaction.InTurn)
	}

	before, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CommitUCICommands(paths, mustParseUCI(t, "set compaction.in_turn.keep_recent_steps=0"))
	if err == nil || !strings.Contains(err.Error(), "in_turn.keep_recent_steps") {
		t.Fatalf("keep_recent_steps=0 must be refused, got %v", err)
	}
	after, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused edit changed the file:\n%s\n---\n%s", before, after)
	}
}

// A save of a config that never named the section does not materialize it, and
// an explicit enable: false is kept.
func TestMarshalConfigYAMLKeepsInTurnOnlyWhenSet(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Agent.MaxTurns = 42
	saved := rewrite(t, cfg, sparseConfig)
	for _, absent := range []string{"in_turn", "keep_recent_steps"} {
		if strings.Contains(saved, absent) {
			t.Errorf("saved config materializes %q the file never had:\n%s", absent, saved)
		}
	}

	const withSwitch = sparseConfig + `compaction:
  in_turn:
    enable: false
`
	cfg = loadForRewrite(t, withSwitch)
	cfg.Agent.MaxTurns = 42
	saved = rewrite(t, cfg, withSwitch)
	if !strings.Contains(saved, "enable: false") || !strings.Contains(saved, "in_turn:") {
		t.Errorf("saved config dropped the explicit in_turn switch:\n%s", saved)
	}
}
