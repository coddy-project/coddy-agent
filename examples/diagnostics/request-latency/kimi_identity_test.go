package main

import (
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestInputForPreservesKimiIdentity(t *testing.T) {
	resolved := &config.ResolvedLLM{
		ProviderName: "kimi", ProviderType: "kimi", Model: "example",
		Mode: "subscription", Region: "china", KimiIdentity: "0123456789abcdef0123456789abcdef",
		AuthPath: "/synthetic/credentials", ProxyURL: "http://127.0.0.1:1234", NoCLILogin: true,
	}
	input := inputFor(resolved, options{timeout: time.Second}, "")
	if input.KimiIdentity != resolved.KimiIdentity || input.AuthPath != resolved.AuthPath || input.Mode != resolved.Mode || input.Region != resolved.Region || input.ProxyURL != resolved.ProxyURL || !input.NoCLILogin {
		t.Fatalf("Kimi provider settings were not preserved: %+v", input)
	}
}
