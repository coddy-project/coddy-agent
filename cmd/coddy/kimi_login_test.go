package main

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestKimiLoginPlatformRefused(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Mode: "platform"}}}
	err := providersLogin(cfg, "kimi", "", false, false, false, false, "", "")
	if err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("expected subscription mode refusal, got %v", err)
	}
}
