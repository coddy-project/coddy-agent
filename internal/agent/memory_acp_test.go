//go:build memory

package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The memory subagent never runs on a model a local ACP agent serves: such an
// agent would take the memory task for the user's request.
func TestMemoryFallbacksLeaveACPModelsOut(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: "claude-agent-acp"}},
			{Name: "fake", Type: "openai", APIKey: "test"},
		},
		Models: []config.ModelEntry{{Model: "agent/default"}, {Model: "fake/small"}, {Model: "fake/big"}},
	}
	got := memoryFallbackModels(cfg, []string{"agent/default", "fake/small"}, "agent/default")
	if strings.Join(got, ",") != "fake/small" {
		t.Fatalf("fallbacks = %v, want only the models that are not agents", got)
	}
	got = memoryFallbackModels(cfg, nil, "fake/big")
	if strings.Join(got, ",") != "fake/big" {
		t.Fatalf("fallbacks = %v, want the session's own model", got)
	}
}
