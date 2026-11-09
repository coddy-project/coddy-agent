//go:build http

package httpserver

import (
	"encoding/json"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"testing"
)

func TestDirectServiceTierParameters(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "api", Type: "openai"}}, Models: []config.ModelEntry{{Model: "api/test"}}}
	for _, tier := range []string{"default", "priority", "fast", "ultrafast"} {
		t.Run(tier, func(t *testing.T) {
			raw := json.RawMessage(`{"model":"api/test","messages":[{"role":"user","content":"hi"}],"service_tier":"` + tier + `"}`)
			if _, err := validateChatParameters(raw, false); err != nil {
				t.Fatal(err)
			}
			var chat chatCompletionRequest
			if err := json.Unmarshal(raw, &chat); err != nil {
				t.Fatal(err)
			}
			opts, err := (&Server{}).directRequestOptions(cfg, "api/test", chat)
			if err != nil {
				t.Fatal(err)
			}
			if opts.ServiceTier != tier {
				t.Fatal(opts.ServiceTier)
			}
			raw = json.RawMessage(`{"model":"api/test","input":"hi","service_tier":"` + tier + `"}`)
			if err := validateResponsesDirectFields(raw); err != nil {
				t.Fatal(err)
			}
			var req responsesDirectRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			opts, err = (&Server{}).responsesGenerationOptions(cfg, "api/test", &req)
			if err != nil {
				t.Fatal(err)
			}
			if opts.ServiceTier != tier {
				t.Fatal(opts.ServiceTier)
			}
		})
	}
}

func TestBareServerUsesSharedCatalogContext(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "api", Type: "openai"}}, Models: []config.ModelEntry{{Model: "api/gpt-6-astra"}}}
	s := &Server{}
	if got := s.contextWindowFor(cfg, "api/gpt-6-astra"); got != 1050000 {
		t.Fatal(got)
	}
	cfg.Models[0].MaxContextTokens = 64000
	if got := s.contextWindowFor(cfg, "api/gpt-6-astra"); got != 64000 {
		t.Fatal(got)
	}
}

func TestResponsesTierFieldsKeepRequestedAndActualDistinct(t *testing.T) {
	object := responsesTierFields(map[string]any{"metadata": map[string]any{"caller": "kept"}}, &llm.Response{RequestedServiceTier: "priority", ActualServiceTier: "default"})
	if object["requested_service_tier"] != "priority" || object["service_tier"] != "default" || object["actual_service_tier"] != "default" {
		t.Fatal(object)
	}
	if _, ok := responsesTierFields(map[string]any{}, &llm.Response{})["service_tier"]; ok {
		t.Fatal("invented actual tier")
	}
}

func TestDirectTierRejectsKnownIncompatibleModel(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}, {Name: "api", Type: "openai"}}, Models: []config.ModelEntry{{Model: "oauth/gpt-6-astra"}, {Model: "api/gpt-6-luna"}}}
	for _, model := range []string{"oauth/gpt-6-astra", "api/gpt-6-luna"} {
		if _, err := (&Server{}).directRequestOptions(cfg, model, chatCompletionRequest{ServiceTier: "ultrafast"}); err == nil {
			t.Fatalf("accepted Ultra on %s", model)
		}
	}
}
