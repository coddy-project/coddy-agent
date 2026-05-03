package skills_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestUninstallRemovesSkillDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Skills.InstallDir = tmp

	skillDir := filepath.Join(tmp, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := skills.Uninstall(cfg, "my-skill"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatal("expected skill dir removed")
	}
}

func TestUninstallNotFound(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Skills.InstallDir = tmp

	err := skills.Uninstall(cfg, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUninstallRejectsPathLikeName(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Skills.InstallDir = tmp

	for _, name := range []string{"a/b", "../x", "", "  "} {
		t.Run(name, func(t *testing.T) {
			err := skills.Uninstall(cfg, name)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
