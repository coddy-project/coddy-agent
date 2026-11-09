package agent

import (
	"context"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"testing"
)

func TestCompactionInheritsExplicitServiceTier(t *testing.T) {
	st := seededCompactState(t, 3)
	st.SetSelectedServiceTier("priority")
	keep := 1
	ag, _, _ := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})
	for i := range ag.cfg.Models {
		ag.cfg.Models[i].ServiceTier = "flex"
	}
	var tiers []string
	inner := ag.providerFactory
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		tiers = append(tiers, in.ServiceTier)
		return inner(in)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{Model: "second", Force: true}); err != nil {
		t.Fatal(err)
	}
	if len(tiers) == 0 {
		t.Fatal("no provider")
	}
	for _, tier := range tiers {
		if tier != "priority" {
			t.Fatalf("tiers=%v", tiers)
		}
	}
}

func TestCodexWorkingWindowIsAlreadyTheInputBudget(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	ag, _, _ := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})
	ag.cfg.Providers = append(ag.cfg.Providers, config.ProviderConfig{Name: "oauth", Type: "codex"})
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "oauth/gpt-6-astra", MaxTokens: 128000})
	if got := ag.inputBudgetFor("oauth/gpt-6-astra", 272000); got != 272000 {
		t.Fatalf("working window reduced to %d", got)
	}
}

func TestExplicitContextWithOmittedOutputKeepsUsableBudget(t *testing.T) {
	st := seededCompactState(t, 3)
	keep := 1
	ag, _, _ := twoModelCompactAgent(t, st, config.Compaction{KeepRecentTurns: &keep})
	ag.cfg.Models = append(ag.cfg.Models, config.ModelEntry{Model: "fake/gpt-6-astra", MaxContextTokens: 64000})
	if got := ag.inputBudgetFor("fake/gpt-6-astra", 64000); got != 64000 {
		t.Fatalf("budget=%d", got)
	}
	ag.cfg.Models[len(ag.cfg.Models)-1].MaxContextTokens = 1500000
	if got := ag.inputBudgetFor("fake/gpt-6-astra", 1500000); got != 1500000 {
		t.Fatalf("explicit override clamped to %d", got)
	}
}
