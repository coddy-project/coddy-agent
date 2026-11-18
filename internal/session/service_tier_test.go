package session

import (
	"context"
	"encoding/json"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"log/slog"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func serviceTierManager(t *testing.T) (*Manager, *State, *config.Config) {
	t.Helper()
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "openai", Type: "openai"}}, Models: []config.ModelEntry{{Model: "openai/gpt-6-astra"}, {Model: "openai/gpt-4o"}}, Agent: config.Agent{Model: "openai/gpt-6-astra"}}
	m := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	st := &State{ID: NewSessionID(), SelectedModelID: cfg.Agent.Model}
	m.sessions[st.ID] = st
	return m, st, cfg
}

func TestServiceTierSettingsAndTurn(t *testing.T) {
	m, st, cfg := serviceTierManager(t)
	tier := "priority"
	snap, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier})
	if err != nil {
		t.Fatal(err)
	}
	if snap.ServiceTier != tier || len(snap.ServiceTierChoices) == 0 {
		t.Fatalf("snapshot=%+v", snap)
	}
	m.beginOperatorTurnSettings(st.ID, st)
	tier = "default"
	if _, err = m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier}); err != nil {
		t.Fatal(err)
	}
	if got := st.EffectiveServiceTier(cfg); got != "priority" {
		t.Fatalf("active tier=%q", got)
	}
	st.EndTurnSettings()
	if got := st.EffectiveServiceTier(cfg); got != "default" {
		t.Fatalf("next tier=%q", got)
	}
	old := st.GetSelectedModelID()
	model := "openai/gpt-4o"
	if _, err = m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{Model: &model, ServiceTier: &tier}); err == nil {
		t.Fatal("accepted incompatible group")
	}
	if st.GetSelectedModelID() != old {
		t.Fatal("partial write")
	}
	if _, err = m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{Model: &model}); err != nil {
		t.Fatal(err)
	}
	if st.GetSelectedServiceTier() != "" {
		t.Fatal("incompatible selection not reset")
	}
}

func TestServiceTierConcurrentSettings(t *testing.T) {
	m, st, _ := serviceTierManager(t)
	var wg sync.WaitGroup
	for _, tier := range []string{"default", "priority", "ultrafast", ""} {
		wg.Go(func() {
			for range 10 {
				if _, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func TestServiceTierPersistence(t *testing.T) {
	_, st, cfg := serviceTierManager(t)
	store := &FileStore{Root: t.TempDir()}
	dir, err := store.EnsureLayout(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.SessionDir = dir
	st.SetSelectedServiceTier("priority")
	st.captureTurnServiceTier(cfg)
	st.SetSelectedServiceTier("default")
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := store.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.SelectedServiceTier != "default" || snap.Meta.TurnServiceTier == nil || snap.Meta.TurnServiceTier.Tier != "priority" {
		t.Fatalf("tier=%q", snap.Meta.SelectedServiceTier)
	}
	m := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), store.Root, store)
	if _, err := m.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: st.ID}); err != nil {
		t.Fatal(err)
	}
	restored := m.SessionByID(st.ID)
	if restored.GetSelectedServiceTier() != "default" {
		t.Fatal("manager lost selected tier")
	}
	restored.ResumeTurnSettings()
	if restored.EffectiveServiceTier(cfg) != "priority" {
		t.Fatal("manager lost continuation tier")
	}
}

func TestServiceTierRestoreAndResume(t *testing.T) {
	m, st, cfg := serviceTierManager(t)
	m.beginOperatorTurnSettings(st.ID, st)
	tier := "priority"
	if _, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier}); err != nil {
		t.Fatal(err)
	}
	if st.EffectiveServiceTier(cfg) != "" {
		t.Fatal("a turn with no tier acquired a later selection")
	}
	kept := st.readProcessSettings()
	restored := &State{}
	restored.RestoreMetaWithoutPersist(ModeAgent, cfg.Agent.Model, "", tier, "")
	restored.restoreProcessSettings(kept)
	restored.ResumeTurnSettings()
	if restored.EffectiveServiceTier(cfg) != "" {
		t.Fatal("resume lost frozen omission")
	}
	restored.EndTurnSettings()
	if restored.EffectiveServiceTier(cfg) != tier {
		t.Fatal("restore lost session preference")
	}
	var old SessionMeta
	if err := json.Unmarshal([]byte(`{"selectedModelId":"openai/gpt-6-astra"}`), &old); err != nil {
		t.Fatal(err)
	}
	restored.RestoreMetaWithoutPersist(ModeAgent, old.SelectedModelID, old.SelectedReasoning, old.SelectedServiceTier, "")
	if restored.GetSelectedServiceTier() != "" {
		t.Fatal("old metadata acquired a tier")
	}
}

func TestServiceTierACPOption(t *testing.T) {
	m, st, _ := serviceTierManager(t)
	initial := BuildACPConfigOptions(m.Cfg(), st)
	for _, opt := range initial {
		if opt.ID != "service_tier" {
			continue
		}
		if opt.CurrentValue != "default" {
			t.Fatalf("unset current value=%q, want Standard", opt.CurrentValue)
		}
		for _, choice := range opt.Options {
			if choice.Value == "" || choice.Name == "Auto" {
				t.Fatalf("legacy empty choice is still offered: %+v", choice)
			}
		}
	}
	result, err := m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: st.ID, ConfigID: "service_tier", Value: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, opt := range result.ConfigOptions {
		if opt.ID == "service_tier" {
			found = true
			if opt.CurrentValue != "priority" || opt.Category != "model_config" {
				t.Fatalf("option=%+v", opt)
			}
		}
	}
	if !found {
		t.Fatal("missing tier option")
	}
	result, err = m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: st.ID, ConfigID: "service_tier", Value: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for _, opt := range result.ConfigOptions {
		if opt.ID == "service_tier" && opt.CurrentValue != "default" {
			t.Fatalf("Standard after Fast=%q", opt.CurrentValue)
		}
	}
	if got := st.GetSelectedServiceTier(); got != "default" {
		t.Fatalf("Standard after Fast stored %q", got)
	}
	if _, err = m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: st.ID, ConfigID: "service_tier", Value: "unknown"}); err == nil {
		t.Fatal("accepted unknown tier")
	}
}

func TestServiceTierEndpointSessionAndFallback(t *testing.T) {
	m, st, cfg := serviceTierManager(t)
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "ollama", Type: "openai", APIBase: "http://localhost:11434/v1"})
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "ollama/gpt-6-astra"})
	tier := "priority"
	if _, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier}); err != nil {
		t.Fatal(err)
	}
	if got := InheritedServiceTier(cfg, "openai/gpt-6-astra", "ollama/gpt-6-astra", tier); got != "" {
		t.Fatalf("fallback inherited %q", got)
	}
	rm := &config.ResolvedLLM{ProviderName: "openai", ProviderType: "openai", Model: "gpt-6-astra", BaseURL: "http://localhost:11434/v1"}
	if got := st.ServiceTierForProvider(cfg, rm); got != nil {
		t.Fatalf("fallback sent %q", *got)
	}
	model := "ollama/gpt-6-astra"
	if _, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{Model: &model}); err != nil {
		t.Fatal(err)
	}
	if st.SessionServiceTier(cfg) != "" {
		t.Fatal("local model retained tier")
	}
	if _, err := m.ApplySessionSettings(context.Background(), st.ID, SettingsChange{ServiceTier: &tier}); err == nil {
		t.Fatal("local model accepted tier")
	}
}
