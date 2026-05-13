package config_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestFindModelEntryRuntimeOverlay(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "static/m1"}},
		RuntimeOverlay: &config.RuntimeOverlay{
			Models: []config.ModelEntry{{Model: "runtime/m2"}},
		},
	}

	if got := cfg.FindModelEntry("static/m1"); got == nil {
		t.Fatal("expected static model to be found")
	}
	if got := cfg.FindModelEntry("runtime/m2"); got == nil {
		t.Fatal("expected runtime model to be found")
	}
	if got := cfg.FindModelEntry("missing/m3"); got != nil {
		t.Fatal("expected missing model to return nil")
	}
}

func TestFindProviderRuntimeOverlay(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "static-p1"}},
		RuntimeOverlay: &config.RuntimeOverlay{
			Providers: []config.ProviderConfig{{Name: "runtime-p2"}},
		},
	}

	if got := cfg.FindProvider("static-p1"); got == nil {
		t.Fatal("expected static provider to be found")
	}
	if got := cfg.FindProvider("runtime-p2"); got == nil {
		t.Fatal("expected runtime provider to be found")
	}
	if got := cfg.FindProvider("missing-p3"); got != nil {
		t.Fatal("expected missing provider to return nil")
	}
}

func TestFindProviderStaticWinsOverRuntime(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "same", Type: "static"}},
		RuntimeOverlay: &config.RuntimeOverlay{
			Providers: []config.ProviderConfig{{Name: "same", Type: "runtime"}},
		},
	}

	got := cfg.FindProvider("same")
	if got == nil {
		t.Fatal("expected provider to be found")
	}
	if got.Type != "static" {
		t.Fatalf("expected static provider to win, got type %q", got.Type)
	}
}

func TestFindModelEntryStaticWinsOverRuntime(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "same", MaxTokens: 1}},
		RuntimeOverlay: &config.RuntimeOverlay{
			Models: []config.ModelEntry{{Model: "same", MaxTokens: 2}},
		},
	}

	got := cfg.FindModelEntry("same")
	if got == nil {
		t.Fatal("expected model to be found")
	}
	if got.MaxTokens != 1 {
		t.Fatalf("expected static model to win, got max_tokens %d", got.MaxTokens)
	}
}

func TestAllProviders(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "static-p1"},
		},
		RuntimeOverlay: &config.RuntimeOverlay{
			Providers: []config.ProviderConfig{
				{Name: "runtime-p2"},
			},
		},
	}

	all := cfg.AllProviders()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
	if all[0].Name != "static-p1" {
		t.Fatalf("expected first provider to be static, got %q", all[0].Name)
	}
	if all[1].Name != "runtime-p2" {
		t.Fatalf("expected second provider to be runtime, got %q", all[1].Name)
	}

	// Ensure original slices are not modified.
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected static providers unchanged, got %d", len(cfg.Providers))
	}
	if len(cfg.RuntimeOverlay.Providers) != 1 {
		t.Fatalf("expected runtime providers unchanged, got %d", len(cfg.RuntimeOverlay.Providers))
	}

	// Ensure mutating returned slice does not affect internal state.
	originalName := cfg.Providers[0].Name
	all[0].Name = "mutated"
	if cfg.Providers[0].Name != originalName {
		t.Errorf("mutating returned slice mutated internal state")
	}
	originalRuntimeName := cfg.RuntimeOverlay.Providers[0].Name
	all[1].Name = "mutated-runtime"
	if cfg.RuntimeOverlay.Providers[0].Name != originalRuntimeName {
		t.Errorf("mutating returned slice mutated runtime overlay internal state")
	}
}

func TestAllModels(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{
			{Model: "static/m1"},
		},
		RuntimeOverlay: &config.RuntimeOverlay{
			Models: []config.ModelEntry{
				{Model: "runtime/m2"},
			},
		},
	}

	all := cfg.AllModels()
	if len(all) != 2 {
		t.Fatalf("expected 2 models, got %d", len(all))
	}
	if all[0].Model != "static/m1" {
		t.Fatalf("expected first model to be static, got %q", all[0].Model)
	}
	if all[1].Model != "runtime/m2" {
		t.Fatalf("expected second model to be runtime, got %q", all[1].Model)
	}

	// Ensure original slices are not modified.
	if len(cfg.Models) != 1 {
		t.Fatalf("expected static models unchanged, got %d", len(cfg.Models))
	}
	if len(cfg.RuntimeOverlay.Models) != 1 {
		t.Fatalf("expected runtime models unchanged, got %d", len(cfg.RuntimeOverlay.Models))
	}

	// Ensure mutating returned slice does not affect internal state.
	originalModel := cfg.Models[0].Model
	all[0].Model = "mutated"
	if cfg.Models[0].Model != originalModel {
		t.Errorf("mutating returned slice mutated internal state")
	}
	originalRuntimeModel := cfg.RuntimeOverlay.Models[0].Model
	all[1].Model = "mutated-runtime"
	if cfg.RuntimeOverlay.Models[0].Model != originalRuntimeModel {
		t.Errorf("mutating returned slice mutated runtime overlay internal state")
	}
}

func TestAllProvidersWithNilOverlay(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "static-p1"}},
	}

	all := cfg.AllProviders()
	if len(all) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(all))
	}
	if all[0].Name != "static-p1" {
		t.Fatalf("expected static provider, got %q", all[0].Name)
	}
}

func TestAllModelsWithNilOverlay(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "static/m1"}},
	}

	all := cfg.AllModels()
	if len(all) != 1 {
		t.Fatalf("expected 1 model, got %d", len(all))
	}
	if all[0].Model != "static/m1" {
		t.Fatalf("expected static model, got %q", all[0].Model)
	}
}
