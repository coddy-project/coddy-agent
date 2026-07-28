package llm

import (
	"context"
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
