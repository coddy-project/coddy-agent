package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The row of a run names the reasoning level before the run's session exists:
// the level must be the one the session then calls its model with.
func TestResolveReasoningLevel(t *testing.T) {
	levels := []string{"low", "medium", "high"}
	none := []string{}
	cfg := &config.Config{
		Agent: config.Agent{Model: "p/think"},
		Models: []config.ModelEntry{
			{Model: "p/think", ReasoningLevels: &levels, ReasoningDefault: "medium"},
			{Model: "p/plain", ReasoningLevels: &none},
		},
	}
	cases := []struct {
		name, model, selected, want string
	}{
		{"the selection the model offers", "p/think", "high", "high"},
		{"no selection takes the model's default", "p/think", "", "medium"},
		{"a level the model does not offer takes the default", "p/think", "max", "medium"},
		{"no model follows the agent model", "", "low", "low"},
		{"a model without levels has none", "p/plain", "high", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveReasoningLevel(cfg, tc.model, tc.selected); got != tc.want {
				t.Fatalf("ResolveReasoningLevel(%q, %q) = %q, want %q", tc.model, tc.selected, got, tc.want)
			}
		})
	}
	if got := ResolveReasoningLevel(nil, "p/think", "high"); got != "" {
		t.Fatalf("without a config = %q, want empty", got)
	}
}
