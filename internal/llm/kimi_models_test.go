package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKimiModelsEndpoints(t *testing.T) {
	for _, tc := range []struct{ mode, region, want string }{
		{"", "", "https://api.kimi.ai/coding/v1/models"}, {"subscription", "china", "https://api.kimi.com/coding/v1/models"}, {"platform", "global", "https://api.moonshot.ai/v1/models"}, {"platform", "china", "https://api.moonshot.cn/v1/models"},
	} {
		got, err := kimiModelsEndpoint(tc.mode, tc.region)
		if err != nil || got != tc.want {
			t.Errorf("%+v: %q %v", tc, got, err)
		}
	}
	if _, err := kimiModelsEndpoint("bad", "global"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := kimiModelsEndpoint("platform", "bad"); err == nil {
		t.Fatal("invalid region accepted")
	}
}

func TestKimiModelsHTTP(t *testing.T) {
	for _, tc := range []struct{ mode, body, protocol string }{
		{"subscription", `{"data":[{"id":"coding","limit":{"context":262144},"protocol":"anthropic","modalities":{"input":["text","image"]},"reasoning":{"type":"both","effort":{"valid":["low","high"],"default":"high"}}}]}`, "anthropic"},
		{"platform", `{"data":[{"id":"coding","context_length":262144,"supports_image_in":true,"think_efforts":{"support":true,"valid_efforts":["low","high"],"default_effort":"high"}}]}`, "kimi"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" || r.URL.Path != "/models" {
					t.Error("bad request")
				}
				if _, err := fmt.Fprint(w, tc.body); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer s.Close()
			models, err := fetchKimiModels(context.Background(), tc.mode, "secret", s.URL+"/models", s.Client())
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 || models[0].ID != "coding" || models[0].ContextWindow != 262144 || models[0].ImageInput == nil || !*models[0].ImageInput || models[0].Protocol != tc.protocol || len(models[0].ReasoningLevels) != 2 || models[0].DefaultReasoning != "high" {
				t.Fatalf("%+v", models)
			}
		})
	}
}
func TestKimiModelsSecurity(t *testing.T) {
	if _, err := fetchKimiModels(context.Background(), "subscription", "secret", "http://example.test", nil); err == nil {
		t.Fatal("nil client accepted")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cross-origin redirect followed") }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	_, err := fetchKimiModels(context.Background(), "subscription", "secret", s.URL, s.Client())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestKimiModelsProxy(t *testing.T) {
	hits := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.Method + " " + r.Host
		http.Error(w, "blocked by test", http.StatusBadGateway)
	}))
	defer proxy.Close()
	_, err := ListKimiModels(context.Background(), "platform", "global", "secret", proxy.URL)
	if err == nil {
		t.Fatal("expected proxy failure")
	}
	select {
	case got := <-hits:
		if got != "CONNECT api.moonshot.ai:443" {
			t.Fatalf("proxy saw %q", got)
		}
	default:
		t.Fatal("proxy not reached")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("credential leaked")
	}
}

func TestKimiModelsInvalidAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`{"data":[{"id":"x","limit":{"context":0}}]}`, true},
		{`{"data":[{"id":"x","limit":{"context":1}},{"id":"x","limit":{"context":1}}]}`, true},
		{`{"data":[{"id":"x","limit":{"context":1},"protocol":"future"}]}`, false},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := fmt.Fprint(w, tc.body); err != nil {
				t.Errorf("write response: %v", err)
			}
		}))
		models, err := fetchKimiModels(context.Background(), "subscription", "secret", s.URL, s.Client())
		s.Close()
		if (err != nil) != tc.bad {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if !tc.bad && (models[0].Protocol != "future" || models[0].ImageInput != nil) {
			t.Fatalf("unknown metadata lost: %+v", models[0])
		}
	}
}

func TestKimiModelsUpstreamMetadata(t *testing.T) {
	for _, tc := range []struct{ name, fields, thinking, protocol, effort string }{
		{"missing", ``, "no", "kimi", ""},
		{"none", `,"reasoning":{"type":"none"}`, "no", "kimi", ""},
		{"response", `,"protocol":"response"`, "no", "openai_responses", ""},
		{"anthropic", `,"protocol":"anthropic"`, "no", "anthropic", ""},
		{"unknown", `,"protocol":"future"`, "no", "future", ""},
		{"default without valid", `,"reasoning":{"type":"both","effort":{"default":"high"}}`, "both", "kimi", "high"},
		{"default with empty valid", `,"reasoning":{"type":"both","effort":{"valid":[],"default":"high"}}`, "both", "kimi", "high"},
		{"default in valid", `,"reasoning":{"type":"only","effort":{"valid":["low","high"],"default":"high"}}`, "only", "kimi", "high"},
		{"default outside valid", `,"reasoning":{"type":"both","effort":{"valid":["low"],"default":"high"}}`, "both", "kimi", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := fmt.Fprintf(w, `{"data":[{"id":"x","limit":{"context":123}%s}]}`, tc.fields); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer s.Close()
			models, err := fetchKimiModels(context.Background(), "subscription", "secret", s.URL, s.Client())
			if err != nil {
				t.Fatal(err)
			}
			m := models[0]
			if m.ThinkingType != tc.thinking || m.Protocol != tc.protocol || m.DefaultReasoning != tc.effort {
				t.Errorf("got thinking=%q protocol=%q effort=%q", m.ThinkingType, m.Protocol, m.DefaultReasoning)
			}
		})
	}
}
