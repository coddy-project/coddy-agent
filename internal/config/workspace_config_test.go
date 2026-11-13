package config

import (
	"os"
	"path/filepath"
	"testing"
)

const looseningBody = `mcp:
  project_trust: allow
hooks:
  project_trust: allow
subagents:
  project_trust: allow
skills:
  project_trust: allow
scheduler:
  project_trust: allow
tools:
  command_allowlist: ["*", "git status"]
`

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A config.yaml in the working directory may have come with a checkout: it
// cannot trust the checkout's own hooks, servers, subagents, skill sources or
// jobs, and cannot let commands run without a question.
func TestAWorkspaceConfigCannotLoosenTrust(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	writeConfig(t, project, looseningBody)

	cfg, err := LoadFromCLI(CLIPaths{Home: home, CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	for key, got := range map[string]string{
		"mcp.project_trust":       cfg.MCP.ProjectTrust,
		"hooks.project_trust":     cfg.Hooks.ProjectTrust,
		"subagents.project_trust": cfg.Subagents.ProjectTrust,
		"skills.project_trust":    cfg.Skills.ProjectTrust,
		"scheduler.project_trust": cfg.Scheduler.ProjectTrust,
	} {
		if got == ProjectTrustAllow {
			t.Errorf("%s = %q from the workspace's config.yaml, want the default", key, got)
		}
	}
	if len(cfg.Tools.CommandAllowlist) != 0 {
		t.Errorf("tools.command_allowlist = %q from the workspace's config.yaml, want none", cfg.Tools.CommandAllowlist)
	}
}

// The operator's own file, and a file named with --config, keep every key.
func TestTheOperatorsConfigKeepsItsTrust(t *testing.T) {
	for name, load := range map[string]func(t *testing.T) (*Config, error){
		"home": func(t *testing.T) (*Config, error) {
			home := t.TempDir()
			writeConfig(t, home, looseningBody)
			return LoadFromCLI(CLIPaths{Home: home, CWD: t.TempDir()})
		},
		"--config": func(t *testing.T) (*Config, error) {
			project := t.TempDir()
			path := writeConfig(t, project, looseningBody)
			return LoadFromCLI(CLIPaths{Home: filepath.Join(t.TempDir(), "home"), CWD: project, Config: path})
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := load(t)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Hooks.ProjectTrust != ProjectTrustAllow || cfg.MCP.ProjectTrust != ProjectTrustAllow {
				t.Fatalf("hooks %q, mcp %q, want allow kept", cfg.Hooks.ProjectTrust, cfg.MCP.ProjectTrust)
			}
			if len(cfg.Tools.CommandAllowlist) != 2 {
				t.Fatalf("tools.command_allowlist = %q, want both entries kept", cfg.Tools.CommandAllowlist)
			}
		})
	}
}
