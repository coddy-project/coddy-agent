package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"reflect"
	"testing"
)

func TestCachedReasoningPrecedence(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}}, Models: []config.ModelEntry{{Model: "oauth/gpt-6-astra", ReasoningDefault: "max"}}}
	mgr := newWindowTestManager(t, cfg, &windowListing{}, nil)
	key := contextWindowCacheKey(cfg, &cfg.Providers[0])
	mgr.windows.entries = map[string]*contextWindowEntry{key: {reasoning: map[string][]string{"gpt-6-astra": {"low", "max"}}}}
	st := &State{SelectedModelID: cfg.Models[0].Model, contextWindows: mgr}
	if got := st.ReasoningChoices(cfg, cfg.Models[0].Model); !reflect.DeepEqual(got, []string{"low", "max"}) {
		t.Fatalf("choices=%v", got)
	}
	high := "high"
	if _, err := mgr.validateSettingsChange(st, SettingsChange{Reasoning: &high}); err == nil {
		t.Fatal("accepted unsupported cached level")
	}
	if got := st.EffectiveReasoning(cfg); got != "max" {
		t.Fatal(got)
	}
	explicit := []string{"high"}
	cfg.Models[0].ReasoningLevels = &explicit
	if got := st.ReasoningChoices(cfg, cfg.Models[0].Model); !reflect.DeepEqual(got, explicit) {
		t.Fatal(got)
	}
	explicit = []string{}
	if got := st.ReasoningChoices(cfg, cfg.Models[0].Model); len(got) != 0 {
		t.Fatal(got)
	}
	cfg.Models[0].ReasoningLevels = nil
	mgr.windows.entries[key].reasoning["gpt-6-astra"] = []string{}
	if got := st.ReasoningChoices(cfg, cfg.Models[0].Model); len(got) != 0 {
		t.Fatal(got)
	}
	if cfg.Models[0].ReasoningLevels != nil {
		t.Fatal("cache mutated configuration")
	}
}

func TestCodexCatalogTierChoices(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}}, Models: []config.ModelEntry{{Model: "oauth/gpt-6-astra"}}}
	mgr := newWindowTestManager(t, cfg, &windowListing{}, nil)
	key := contextWindowCacheKey(cfg, &cfg.Providers[0])
	mgr.windows.entries = map[string]*contextWindowEntry{key: {tiers: map[string][]string{"gpt-6-astra": {"priority"}}}}
	st := &State{contextWindows: mgr, SelectedServiceTier: "ultrafast"}
	if got := st.ServiceTiers(cfg, cfg.Models[0].Model); !reflect.DeepEqual(got, []string{"default", "priority"}) {
		t.Fatal(got)
	}
	if tier, fallback := st.ServiceTierFor(cfg, cfg.Models[0].Model); tier != "default" || !fallback {
		t.Fatalf("%s/%v", tier, fallback)
	}
	mgr.windows.entries[key].tiers["gpt-6-astra"] = []string{}
	if st.ServiceTierSupported(cfg, cfg.Models[0].Model, "priority") {
		t.Fatal("ignored explicit empty catalog tiers")
	}
}

func TestAccountChangeDropsCachedCapabilities(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}}, Models: []config.ModelEntry{{Model: "oauth/gpt-6-astra"}}}
	mgr := newWindowTestManager(t, cfg, &windowListing{}, nil)
	key := contextWindowCacheKey(cfg, &cfg.Providers[0])
	mgr.windows.entries = map[string]*contextWindowEntry{key: {provider: "oauth", reasoning: map[string][]string{"gpt-6-astra": {"low"}}, tiers: map[string][]string{"gpt-6-astra": {}}}}
	mgr.ForgetContextWindows("oauth")
	if mgr.windows.entries[key].reasoning != nil || mgr.windows.entries[key].tiers != nil {
		t.Fatal("previous account capabilities survived invalidation")
	}
}
