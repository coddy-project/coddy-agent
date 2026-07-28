package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

func TestRunPluginCommandUsageAndErrors(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	ctx := context.Background()

	out, err := RunPluginCommand(ctx, cfg, ".", nil)
	if err != nil || !strings.Contains(out, "plugin commands:") {
		t.Fatalf("help: out=%q err=%v", out, err)
	}
	out, err = RunPluginCommand(ctx, cfg, ".", []string{"help"})
	if err != nil || !strings.Contains(out, "marketplace") {
		t.Fatalf("help word: out=%q err=%v", out, err)
	}

	cases := [][]string{
		{"frobnicate"},
		{"marketplace"},
		{"marketplace", "frob"},
		{"marketplace", "add"},
		{"marketplace", "remove"},
		{"install"},
		{"remove"},
		{"enable"},
		{"disable"},
	}
	for _, args := range cases {
		if _, err := RunPluginCommand(ctx, cfg, ".", args); err == nil {
			t.Errorf("expected error for args %v", args)
		}
	}
}

func TestMarketplaceStatusInvalidAndEmpty(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}}
	// No sources.
	if got := MarketplaceStatus(context.Background(), cfg); len(got) != 0 {
		t.Fatalf("expected no statuses, got %v", got)
	}
	// An unparsable source is reported as invalid, not a crash.
	cfg.Skills.Sources = []string{"this is not valid!!"}
	got := MarketplaceStatus(context.Background(), cfg)
	if len(got) != 1 || got[0].Standard != "invalid" || got[0].Valid {
		t.Fatalf("expected one invalid status, got %+v", got)
	}
}

func TestPluginMarketplaceLifecycleFromLocalGit(t *testing.T) {
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	writeMarketplaceManifest(t, repo, "demo", "1.0.0")
	gitCommitAllRepo(t, repo, true, "v1")

	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, ConfigPath: cfgPath},
		Skills: config.Skills{Dirs: []string{filepath.Join(home, "skills")}},
	}
	ctx := context.Background()
	fileURL := "file://" + filepath.ToSlash(repo)

	// install adds the source and materializes the skill.
	out, err := RunPluginCommand(ctx, cfg, ".", []string{"install", fileURL})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "Installed") || !strings.Contains(out, "1 added") {
		t.Fatalf("install output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("skill not materialized: %v", err)
	}

	// marketplace list reports a valid agents-standard marketplace.
	out, err = RunPluginCommand(ctx, cfg, ".", []string{"marketplace", "list"})
	if err != nil {
		t.Fatalf("marketplace list: %v", err)
	}
	if !strings.Contains(out, "valid marketplace") || !strings.Contains(out, "1 plugin(s)") {
		t.Fatalf("marketplace list output: %q", out)
	}

	// plugin list shows the installed skill with its version.
	out, err = RunPluginCommand(ctx, cfg, ".", []string{"list"})
	if err != nil {
		t.Fatalf("plugin list: %v", err)
	}
	if !strings.Contains(out, "demo@1.0.0") {
		t.Fatalf("plugin list output: %q", out)
	}

	// marketplace remove drops the source.
	out, err = RunPluginCommand(ctx, cfg, ".", []string{"marketplace", "remove", fileURL})
	if err != nil {
		t.Fatalf("marketplace remove: %v", err)
	}
	if !strings.Contains(out, "Removed marketplace") {
		t.Fatalf("marketplace remove output: %q", out)
	}
	if len(ListSources(cfg)) != 0 {
		t.Fatalf("source not removed: %v", ListSources(cfg))
	}
}
