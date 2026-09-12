package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestPromptsPerProviderDefaultsToEnabled(t *testing.T) {
	var p config.Prompts
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !p.PerProviderEnabled() {
		t.Error("model-tuned prompts should default to enabled")
	}
	if p.PerProvider.Enabled != nil {
		t.Error("an omitted per_provider.enable must stay unset, so a saved config does not grow the key")
	}
}

func TestPromptsPerProviderExplicitFalsePreserved(t *testing.T) {
	off := false
	p := config.Prompts{PerProvider: config.PerProviderPrompts{Enabled: &off}}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.PerProviderEnabled() {
		t.Error("explicit per_provider.enable=false must be preserved")
	}
}

func TestPromptsPerProviderLoadsFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("prompts:\n  per_provider:\n    enable: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Prompts.PerProviderEnabled() {
		t.Fatal("prompts.per_provider.enable: false must switch model-tuned prompts off")
	}
}

func TestPromptsPerProviderJSONRoundTrip(t *testing.T) {
	off := false
	c := &config.Config{Prompts: config.Prompts{PerProvider: config.PerProviderPrompts{Enabled: &off}}}
	back := config.JSONDTOToConfig(config.ConfigToJSONDTO(c), config.Paths{})
	if back.Prompts.PerProviderEnabled() {
		t.Fatal("per_provider.enable=false must survive the JSON DTO round-trip")
	}

	unset := config.JSONDTOToConfig(config.ConfigToJSONDTO(&config.Config{}), config.Paths{})
	if unset.Prompts.PerProvider.Enabled != nil || !unset.Prompts.PerProviderEnabled() {
		t.Fatal("an unset per_provider.enable must stay unset (default on) through the JSON DTO")
	}
}
