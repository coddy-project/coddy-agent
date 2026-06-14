package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func reasoningTestCfg() *config.Config {
	return &config.Config{
		Models: []config.ModelEntry{
			{Model: "openai/gpt-5", ReasoningDefault: "medium"},
			{Model: "openai/gpt-4o"},
		},
		Agent: config.Agent{Model: "openai/gpt-5"},
	}
}

func TestEffectiveReasoningSelectionWins(t *testing.T) {
	cfg := reasoningTestCfg()
	st := &State{SelectedModelID: "openai/gpt-5"}
	st.SetSelectedReasoning("high")
	if got := st.GetSelectedReasoning(); got != "high" {
		t.Fatalf("GetSelectedReasoning = %q, want high", got)
	}
	if got := st.EffectiveReasoning(cfg); got != "high" {
		t.Errorf("EffectiveReasoning = %q, want high", got)
	}
}

func TestEffectiveReasoningFallsBackToModelDefault(t *testing.T) {
	cfg := reasoningTestCfg()
	st := &State{SelectedModelID: "openai/gpt-5"}
	// "bogus" is not a valid level for gpt-5 -> fall back to model default "medium".
	st.SetSelectedReasoning("bogus")
	if got := st.EffectiveReasoning(cfg); got != "medium" {
		t.Errorf("EffectiveReasoning = %q, want medium (model default)", got)
	}
}

func TestEffectiveReasoningNonReasoningModel(t *testing.T) {
	cfg := reasoningTestCfg()
	st := &State{SelectedModelID: "openai/gpt-4o"}
	// Even with a selection, a non-reasoning model never sends a reasoning level.
	st.SetSelectedReasoning("high")
	if got := st.EffectiveReasoning(cfg); got != "" {
		t.Errorf("EffectiveReasoning = %q, want empty for non-reasoning model", got)
	}
}
