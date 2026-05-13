package config_test

import (
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestRuntimeOverlayRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ui-config.yaml")

	original := &config.RuntimeOverlay{
		Providers: []config.ProviderConfig{
			{
				Name:    "openai",
				Type:    "openai",
				APIBase: "https://api.openai.com/v1",
				APIKey:  "sk-test",
			},
		},
		Models: []config.ModelEntry{
			{
				Model:            "openai/gpt-4",
				MaxTokens:        4096,
				Temperature:      0.7,
				MaxContextTokens: 8192,
			},
		},
	}

	if err := config.SaveRuntimeOverlay(path, original); err != nil {
		t.Fatalf("SaveRuntimeOverlay error: %v", err)
	}

	loaded, err := config.LoadRuntimeOverlay(path)
	if err != nil {
		t.Fatalf("LoadRuntimeOverlay error: %v", err)
	}

	if len(loaded.Providers) != len(original.Providers) {
		t.Fatalf("Providers length mismatch: got %d, want %d", len(loaded.Providers), len(original.Providers))
	}
	for i, got := range loaded.Providers {
		want := original.Providers[i]
		if got.Name != want.Name || got.Type != want.Type || got.APIBase != want.APIBase || got.APIKey != want.APIKey {
			t.Fatalf("Provider[%d] mismatch: got %+v, want %+v", i, got, want)
		}
	}

	if len(loaded.Models) != len(original.Models) {
		t.Fatalf("Models length mismatch: got %d, want %d", len(loaded.Models), len(original.Models))
	}
	for i, got := range loaded.Models {
		want := original.Models[i]
		if got.Model != want.Model || got.MaxTokens != want.MaxTokens || got.Temperature != want.Temperature || got.MaxContextTokens != want.MaxContextTokens {
			t.Fatalf("Model[%d] mismatch: got %+v, want %+v", i, got, want)
		}
	}
}

func TestLoadRuntimeOverlayMissing(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nonexistent", "ui-config.yaml")

	overlay, err := config.LoadRuntimeOverlay(path)
	if err != nil {
		t.Fatalf("LoadRuntimeOverlay missing file should not error, got: %v", err)
	}
	if overlay == nil {
		t.Fatal("LoadRuntimeOverlay missing file should return non-nil empty overlay")
	}
	if len(overlay.Providers) != 0 || len(overlay.Models) != 0 {
		t.Fatalf("Expected empty overlay, got Providers=%d Models=%d", len(overlay.Providers), len(overlay.Models))
	}
}
