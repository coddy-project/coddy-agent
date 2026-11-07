package config

import (
	"path/filepath"
	"testing"
)

func TestKimiProviderAuthPath(t *testing.T) {
	home := t.TempDir()
	id := "0123456789abcdef0123456789abcdef"
	if got, want := ProviderConfigAuthPath(home, ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}), filepath.Join(home, "providers", "kimi-identities", id); got != want {
		t.Fatalf("Kimi credential directory = %q, want %q", got, want)
	}
	if got := ProviderAuthPath(home, "../bad", "kimi"); got != "" {
		t.Fatalf("invalid provider name returned path %q", got)
	}
	if got := ProviderAuthPath("", "kimi", "kimi"); got != "" {
		t.Fatalf("empty home returned path %q", got)
	}
}
