package main

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The binary registers the tool catalog itself, in main, instead of a package
// doing it from an init: with it a toolset naming a tool that does not exist
// is reported, without it nothing is.
func TestWireToolCatalogLetsTheConfigCheckNameAnUnknownTool(t *testing.T) {
	cfg := &config.Config{Toolsets: []config.Toolset{{
		Name:  "coding",
		Tools: []string{"read", "gerp", "context7__*"},
	}}}
	config.RegisterToolCatalog(nil)
	t.Cleanup(func() { config.RegisterToolCatalog(nil) })
	if got := cfg.UnknownToolsetTools(); len(got) != 0 {
		t.Fatalf("UnknownToolsetTools without a catalog = %+v, want none", got)
	}

	wireToolCatalog()
	got := cfg.UnknownToolsetTools()
	if len(got) != 1 || got[0].Name != "gerp" {
		t.Fatalf("UnknownToolsetTools = %+v, want only gerp", got)
	}
}
