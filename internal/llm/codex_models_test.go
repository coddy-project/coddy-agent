package llm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListCodexModelsFromCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	cache := `{
      "fetched_at": "2026-07-17T00:00:00Z",
      "models": [
        {"slug": "gpt-5.6-sol", "display_name": "GPT-5.6-Sol"},
        {"slug": "gpt-5.5", "display_name": "GPT-5.5"},
        {"slug": "gpt-5.6-sol", "display_name": "dup ignored"},
        {"slug": "", "display_name": "empty ignored"}
      ]
    }`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	models, err := ListModels(context.Background(), ProviderInput{Type: "codex"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(models), models)
	}
	// Sorted by id: gpt-5.5 before gpt-5.6-sol.
	if models[0].ID != "gpt-5.5" || models[1].ID != "gpt-5.6-sol" {
		t.Errorf("unexpected order/ids: %+v", models)
	}
	if models[1].Name != "GPT-5.6-Sol" {
		t.Errorf("name = %q, want GPT-5.6-Sol", models[1].Name)
	}
}

func TestListCodexModelsMissingCache(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, err := ListModels(context.Background(), ProviderInput{Type: "codex"}); err == nil {
		t.Fatal("expected error for missing models cache, got nil")
	}
}

func TestListCodexModelsHidesTheModelsCodexHides(t *testing.T) {
	// Codex puts a catalog row in its own picker only when its visibility is
	// "list": "hide" marks gpt-reserve and codex-auto-review, and "none" is
	// the third value of the same enum (codex-rs/protocol, ModelVisibility).
	// An absent field is not a hidden model: older caches carry no visibility
	// at all.
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	cache := `{
      "models": [
        {"slug": "gpt-6-astra", "display_name": "GPT-6-Astra", "visibility": "list", "priority": 1},
        {"slug": "gpt-reserve", "display_name": "GPT-Reserve", "visibility": "hide", "priority": 3},
        {"slug": "gpt-internal", "display_name": "GPT-Internal", "visibility": "none", "priority": 2},
        {"slug": "codex-auto-review", "display_name": "Codex Auto Review", "visibility": "hide", "priority": 43},
        {"slug": "gpt-5.5", "display_name": "GPT-5.5"}
      ]
    }`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	models, err := ListModels(context.Background(), ProviderInput{Type: "codex"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	got := make([]string, 0, len(models))
	for _, m := range models {
		got = append(got, m.ID)
	}
	// Still sorted by id, as every other provider's catalog is.
	want := []string{"gpt-5.5", "gpt-6-astra"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %v, want %v", got, want)
		}
	}
}

// The Codex catalog reports each model's window as context_window (272000 for
// every model it serves on 2026-09-27), the one Codex itself works with;
// max_context_window is only the ceiling an operator may raise it to. A value
// in a shape the listing does not expect reads as no window rather than
// failing the whole catalog.
func TestListCodexModelsCarryTheCatalogContextWindow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	cache := `{
      "models": [
        {"slug": "gpt-6-astra", "visibility": "list", "context_window": 272000, "max_context_window": 872000},
        {"slug": "gpt-5.5", "visibility": "list", "context_window": "272000"},
        {"slug": "gpt-odd", "visibility": "list", "context_window": {"tokens": 1}},
        {"slug": "gpt-old", "visibility": "list"}
      ]
    }`
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	models, err := ListModels(context.Background(), ProviderInput{Type: "codex"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	got := make(map[string]int, len(models))
	for _, m := range models {
		got[m.ID] = m.ContextWindow
	}
	want := map[string]int{"gpt-6-astra": 272000, "gpt-5.5": 272000, "gpt-odd": 0, "gpt-old": 0}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("%s: context window = %d, want %d", id, got[id], n)
		}
	}
}

// The model a fresh sign-in adopts as agent.model is the first one Codex
// itself would offer, so a row Codex keeps out of its picker never becomes
// the default, however it is ranked.
func TestRankedCodexModelsOfferOnlyWhatCodexLists(t *testing.T) {
	ranked := rankedCodexModels([]codexModelCacheEntry{
		{Slug: "gpt-internal", Visibility: "none", Priority: 0},
		{Slug: "gpt-reserve", Visibility: "hide", Priority: 1},
		{Slug: "gpt-legacy", Priority: 7},
		{Slug: "gpt-6-astra", Visibility: "list", Priority: 5},
	})
	var got []string
	for _, m := range ranked {
		got = append(got, m.Slug)
	}
	if want := []string{"gpt-6-astra", "gpt-legacy"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ranked = %v, want %v", got, want)
	}
}

// Codex catalog snapshots advertise Fast as priority, independently of the
// API-key model's Ultra capability. Empty and absent lists remain distinct.
func TestCodexCatalogCarriesTierAndReasoningCapabilities(t *testing.T) {
	var rows []codexModelCacheEntry
	raw := `[{"slug":"gpt-6-astra","service_tiers":[{"id":"priority","name":"Fast"}],"supported_reasoning_levels":[{"effort":"low"},{"effort":"xhigh"},{"effort":"max"}]},{"slug":"limited","service_tiers":[],"supported_reasoning_levels":[]},{"slug":"legacy"}]`
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	got := normalizeCodexModels(rows)
	if got[0].ServiceTiers == nil || len(*got[0].ServiceTiers) != 1 || (*got[0].ServiceTiers)[0] != "priority" {
		t.Fatalf("tiers=%+v", got[0])
	}
	if got[0].ReasoningLevels == nil || len(*got[0].ReasoningLevels) != 3 {
		t.Fatal("reasoning capabilities lost")
	}
	if got[1].ServiceTiers != nil || got[2].ServiceTiers == nil || len(*got[2].ServiceTiers) != 0 {
		t.Fatal("empty/absent tiers collapsed")
	}
}
