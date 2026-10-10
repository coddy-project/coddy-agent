package tools

import (
	"sort"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestKnownToolNamesCoverTheBuiltIns(t *testing.T) {
	names := KnownToolNames()
	if !sort.StringsAreSorted(names) {
		t.Fatalf("names are not sorted: %v", names)
	}
	have := map[string]bool{}
	for _, n := range names {
		if have[n] {
			t.Fatalf("%s listed twice", n)
		}
		have[n] = true
	}
	// switch_model is registered only for a configuration with something to
	// switch to, and spawn_agent, load_skill and the background family only
	// while their switches are on: all of them are names a models[] row may
	// mean.
	for _, want := range []string{"read", "write", "edit", "apply_patch", "grep", "glob", "print_tree", "keep_result", "run_command",
		"background_output", "background_wait", "background_stop", "compact_context", "spawn_agent", "load_skill", "switch_model", "websearch", "webfetch"} {
		if !have[want] {
			t.Errorf("KnownToolNames lacks %q", want)
		}
	}
	for n := range have {
		if strings.Contains(n, "__") {
			t.Errorf("%q looks like an MCP tool; those are known only while their server is connected", n)
		}
	}
}

// The config check learns the names from this package at start-up, so a model
// row naming a tool that does not exist is reported without config importing
// upward.
func TestKnownToolNamesAreRegisteredWithTheConfigCheck(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelEntry{{Model: "local/qwen", Tools: []string{"read", "gerp"}}}}
	unknown := cfg.UnknownModelTools()
	if len(unknown) != 1 || unknown[0].Name != "gerp" {
		t.Fatalf("UnknownModelTools = %+v, want only gerp", unknown)
	}
}
