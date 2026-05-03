package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestResolveCODDYHomeEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(config.EnvCODDYHome, tmp)
	t.Setenv(config.EnvCODDYCWD, "")
	t.Setenv(config.EnvCODDYConfig, "")

	p, err := config.Resolve(config.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Clean(p.Home), filepath.Clean(tmp); got != want {
		t.Fatalf("Home %q want %q", got, want)
	}
	if !filepath.IsAbs(p.CWD) {
		t.Fatalf("CWD not absolute: %q", p.CWD)
	}
	wantCfg := filepath.Join(filepath.Clean(tmp), "config.yaml")
	if got := filepath.Clean(p.ConfigPath); got != wantCfg {
		t.Fatalf("ConfigPath %q want %q", got, wantCfg)
	}
}

func TestResolvedSessionsRootDefault(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Paths: config.Paths{Home: home},
	}
	got := cfg.ResolvedSessionsRoot()
	want := filepath.Join(home, "sessions")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvedSessionsRootOverride(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "alt")
	cfg := &config.Config{
		Paths:       config.Paths{Home: t.TempDir()},
		SessionsDir: tmp,
	}
	if got := cfg.ResolvedSessionsRoot(); filepath.Clean(got) != filepath.Clean(tmp) {
		t.Fatalf("got %q", got)
	}
}

func TestExpandCODDYHomeOnlyLeavesCWD(t *testing.T) {
	p := config.Paths{Home: "/h", CWD: "/launch"}
	s := config.ExpandCODDYHomeOnly("${CODDY_HOME}/x ${CWD}/y", p)
	if s != "/h/x ${CWD}/y" {
		t.Fatalf("got %q", s)
	}
}

func TestLoadFromCLIImplicitMissingFileUsesDefaults(t *testing.T) {
	t.Setenv(config.EnvCODDYHome, t.TempDir())
	t.Setenv(config.EnvCODDYConfig, filepath.Join(t.TempDir(), "nope.yaml"))

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.React.MaxTurns != 30 {
		t.Fatalf("defaults not applied, max_turns=%d", cfg.React.MaxTurns)
	}
}

func TestLoadFromFileSetsPaths(t *testing.T) {
	content := `models:
  default: "openai/gpt-4o"
  definitions:
    - id: "openai/gpt-4o"
      provider: "openai"
      model: "gpt-4o"
      api_key: "k"
      max_tokens: 4096
      temperature: 0.1
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.ConfigPath == "" {
		t.Fatal("expected Paths.ConfigPath set")
	}
}
