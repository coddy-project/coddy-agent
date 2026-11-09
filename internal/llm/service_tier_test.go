package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestServiceTierFallbackWire(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		for _, tc := range []struct {
			name   string
			status int
			body   string
			retry  bool
		}{
			{"unsupported", 400, `{"error":{"param":"service_tier","code":"unsupported_value"}}`, true},
			{"tier quota", 429, `{"error":{"code":"service_tier_quota_exhausted"}}`, true},
			{"generic quota", 429, `{"error":{"code":"insufficient_quota"}}`, false},
			{"auth", 401, `{"error":{"code":"invalid_api_key"}}`, false},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				var tiers []string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					var body map[string]any
					_ = json.Unmarshal(raw, &body)
					tier, _ := body["service_tier"].(string)
					tiers = append(tiers, tier)
					if len(tiers) == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tc.status)
						_, _ = fmt.Fprint(w, tc.body)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if protocol == "chat" {
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
					}
				}))
				defer srv.Close()
				p, err := NewProvider(ProviderInput{Type: "openai", Model: "test", Protocol: protocol, ServiceTier: "priority", BaseURL: srv.URL, RetryDisabled: true})
				if err != nil {
					t.Fatal(err)
				}
				_, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
				want := []string{"priority"}
				if tc.retry {
					want = append(want, "default")
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("expected error")
				}
				if !reflect.DeepEqual(tiers, want) {
					t.Fatalf("tiers=%v, want %v", tiers, want)
				}
			})
		}
	}
}

func TestServiceTierNeverRetriesPartialStream(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				if protocol == "chat" {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
				} else {
					_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
				}
				_, _ = fmt.Fprint(w, "data: {\"error\":{\"code\":\"service_tier_quota_exhausted\"}}\n\n")
			}))
			defer srv.Close()
			p, err := NewProvider(ProviderInput{Type: "openai", Model: "test", Protocol: protocol, ServiceTier: "priority", BaseURL: srv.URL, RetryDisabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var text string
			_, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) { text += c.TextDelta })
			if err == nil || calls != 1 || text != "partial" {
				t.Fatalf("err=%v calls=%d text=%q", err, calls, text)
			}
		})
	}
}

func TestServiceTierFallbackOnlyOnceWithRetriesEnabled(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = fmt.Fprint(w, `{"error":{"code":"service_tier_quota_exhausted"}}`)
	}))
	defer srv.Close()
	p, err := NewProvider(ProviderInput{Type: "openai", Model: "test", ServiceTier: "priority", BaseURL: srv.URL, RetryMax: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err == nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestTierFallbackIncompleteResponseKeepsActualTier(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = fmt.Fprint(w, `{"error":{"param":"service_tier","code":"unsupported_value"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "partial"})
		sse(w, "response.incomplete", map[string]any{"response": map[string]any{"status": "incomplete", "service_tier": "default", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": map[string]any{"input_tokens": 2, "output_tokens": 3}}})
	}))
	defer srv.Close()
	p, err := NewProvider(ProviderInput{Type: "openai", Model: "gpt-6-astra", ServiceTier: "priority", BaseURL: srv.URL, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.RequestedServiceTier != "priority" || out.ActualServiceTier != "default" || out.Content != "partial" || out.StopReason != "max_tokens" || out.OutputTokens != 3 {
		t.Fatalf("calls=%d response=%+v", calls, out)
	}
}
