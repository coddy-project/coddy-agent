package agent

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestServiceTierProviderInheritance(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "one", Type: "openai"}, {Name: "two", Type: "openai"}},
		Models:    []config.ModelEntry{{Model: "one/gpt-6-astra"}, {Model: "two/gpt-6-astra"}, {Model: "one/gpt-6-luna"}},
		Agent:     config.Agent{Model: "one/gpt-6-astra"},
	}
	st := &session.State{SelectedModelID: "one/gpt-6-astra", SelectedServiceTier: "priority"}
	a := &Agent{cfg: cfg, state: st}
	for _, id := range []string{"one/gpt-6-astra", "two/gpt-6-astra", "one/gpt-6-luna"} {
		rm, err := cfg.ResolveLLM(id)
		if err != nil {
			t.Fatal(err)
		}
		in := a.llmProviderInput(rm)
		if id == "one/gpt-6-astra" {
			if in.ServiceTier == nil || *in.ServiceTier != "priority" {
				t.Fatal("same pair lost tier")
			}
		} else if in.ServiceTier != nil {
			t.Fatalf("%s inherited tier", id)
		}
	}
}
