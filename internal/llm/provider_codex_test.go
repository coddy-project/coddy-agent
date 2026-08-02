package llm

import (
	"path/filepath"
	"testing"
)

func TestNewProviderCodexUsesManagedAuthAndFixedBackend(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "codex-auth.json")
	provider, err := NewProvider(ProviderInput{
		Type:     "codex",
		Model:    "gpt-test",
		AuthPath: authPath,
		BaseURL:  "https://must-not-receive-oauth.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	resilient, ok := provider.(*resilientProvider)
	if !ok {
		t.Fatalf("provider type = %T", provider)
	}
	codex, ok := resilient.inner.(*codexProvider)
	if !ok {
		t.Fatalf("inner provider type = %T", resilient.inner)
	}
	if codex.auth.path != authPath {
		t.Fatalf("auth path = %q, want %q", codex.auth.path, authPath)
	}
	if codex.baseURL != codexDefaultBaseURL {
		t.Fatalf("base URL = %q, want fixed official backend", codex.baseURL)
	}
}
