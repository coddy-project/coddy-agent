package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestModelServiceTierChoicesEndpoint(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "test", Type: "openai", APIBase: "https://api.openai.com/v1"}}, Models: []config.ModelEntry{{Model: "test/gpt-6-astra"}}}
	if len(ModelServiceTierChoices(cfg, "test/gpt-6-astra")) == 0 {
		t.Fatal("official endpoint should offer tiers")
	}
	cfg.Providers[0].APIBase = "http://localhost:11434/v1"
	if got := ModelServiceTierChoices(cfg, "test/gpt-6-astra"); len(got) != 0 {
		t.Fatalf("localhost offers tiers: %v", got)
	}
}

func TestServiceTierEndpointBoundary(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://localhost:11434/v1")
	for _, tc := range []struct {
		typ, base string
		want      bool
	}{
		{"openai", "", false}, {"openai", "https://api.openai.com/v1", true},
		{"openai", "https://api.openai.com/v1/", true}, {"openai", "https://api.openai.com.evil.test/v1", false},
		{"openai", "http://api.openai.com/v1", false}, {"openai", "https://api.openai.com/v1/other", false},
		{"openai", "https://proxy.example/v1", false}, {"codex", "http://localhost:1234", true},
	} {
		if got := ServiceTierSupported(tc.typ, tc.base); got != tc.want {
			t.Errorf("%s %q: got %v", tc.typ, tc.base, got)
		}
	}
}

type tierRoundTripper func(*http.Request) (*http.Response, error)

func (f tierRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceTierOfficialOutbound(t *testing.T) {
	tier := "priority"
	client := &http.Client{Transport: tierRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["service_tier"] != tier {
			t.Errorf("body=%v", body)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)), Request: r}, nil
	})}
	base := "https://api.openai.com/v1"
	provider, err := NewProvider(ProviderInput{Type: "openai", Model: "gpt-6-astra", APIKey: "test", BaseURL: base, ProxyURL: "none", ServiceTier: &tier, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*resilientProvider).inner.(*openAIProvider)
	p.client = newOpenAIProvider("gpt-6-astra", "test", base, client, 100, 0, "", "").client
	if _, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
}
