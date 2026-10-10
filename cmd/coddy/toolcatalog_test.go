package main

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The binary registers the tool catalog itself, in main, instead of a package
// doing it from an init: with it a model row naming a tool that does not exist
// is reported, without it nothing is.
func TestWireToolCatalogLetsTheConfigCheckNameAnUnknownTool(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelEntry{{
		Model: "local/qwen",
		Tools: []string{"read", "gerp", "context7__*"},
	}}}
	config.RegisterToolCatalog(nil)
	t.Cleanup(func() { config.RegisterToolCatalog(nil) })
	if got := cfg.UnknownModelTools(); len(got) != 0 {
		t.Fatalf("UnknownModelTools without a catalog = %+v, want none", got)
	}

	wireToolCatalog()
	got := cfg.UnknownModelTools()
	if len(got) != 1 || got[0].Name != "gerp" {
		t.Fatalf("UnknownModelTools = %+v, want only gerp", got)
	}
}
