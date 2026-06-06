package skills_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestEnableDisableRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = tmp

	if err := skills.Disable(cfg, "my-skill"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := skills.Enable(cfg, "my-skill"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	managedDir := cfg.Skills.ManagedDir(tmp)
	disabled := skills.ReadDisabled(managedDir)
	if skills.IsDisabled(disabled, "my-skill") {
		t.Error("skill should be enabled after Enable()")
	}
}

func TestEnableDisableRejectsPathLikeName(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = tmp

	for _, name := range []string{"a/b", "../x", "", "  "} {
		t.Run(name, func(t *testing.T) {
			if err := skills.Disable(cfg, name); err == nil {
				t.Fatal("expected error for invalid skill name")
			}
		})
	}
}
