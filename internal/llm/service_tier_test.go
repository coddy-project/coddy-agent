package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestServiceTierTransport(t *testing.T) {
	for _, transport := range []string{"openai", "codex"} {
		for _, stream := range []bool{false, true} {
			for _, requested := range []*string{nil, ptrTier(""), ptrTier("default"), ptrTier("priority"), ptrTier("unknown-tier")} {
				name := "absent"
				if requested != nil {
					name = *requested
				}
				t.Run(fmt.Sprintf("%s/stream=%v/%s", transport, stream, name), func(t *testing.T) {
					var got map[string]any
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Error(err)
						}
						if transport == "codex" {
							w.Header().Set("Content-Type", "text/event-stream")
							sse(w, "response.created", map[string]any{"response": map[string]any{"service_tier": "auto"}})
							sse(w, "response.output_text.delta", map[string]any{"delta": "OK"})
							sse(w, "response.completed", map[string]any{"response": map[string]any{"service_tier": "default", "status": "completed"}})
						} else if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprint(w, "data: {\"service_tier\":\"default\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = fmt.Fprint(w, `{"service_tier":"default","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
						}
					}))
					defer srv.Close()
					in := ProviderInput{Type: transport, Model: "gpt-6-astra", BaseURL: srv.URL, APIKey: "test", ProxyURL: "none", RetryDisabled: true}
					RequestOptions{ServiceTier: requested}.Apply(&in)
					if transport == "codex" {
						p := newCodexTestProvider(t, srv.URL)
						in.AuthPath = p.auth.path
						t.Setenv(EnvCodexBaseURL, srv.URL)
					}
					p, err := NewProvider(in)
					if err != nil {
						t.Fatal(err)
					}
					var resp *Response
					messages := []Message{{Role: RoleUser, Content: "Hi"}}
					if stream {
						resp, err = p.Stream(context.Background(), messages, nil, func(StreamChunk) {})
					} else {
						resp, err = p.Complete(context.Background(), messages, nil)
					}
					if err != nil {
						t.Fatal(err)
					}
					value, present := got["service_tier"]
					if requested == nil {
						if present {
							t.Fatalf("unexpected tier %v", value)
						}
					} else if !present || value != *requested {
						t.Fatalf("tier=%v present=%v want=%q", value, present, *requested)
					}
					if resp.ServiceTier != "default" {
						t.Fatalf("actual tier=%q", resp.ServiceTier)
					}
				})
			}
		}
	}
}

func ptrTier(s string) *string { return &s }

func TestServiceTierImageGeneration(t *testing.T) {
	for _, tier := range []*string{nil, ptrTier(""), ptrTier("priority")} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			value, present := body["service_tier"]
			if tier == nil {
				if present {
					t.Errorf("unexpected tier %v", value)
				}
			} else if !present || value != *tier {
				t.Errorf("tier=%v present=%v want=%q", value, present, *tier)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, imageEvent(generatedPNG(t)))
			sse(w, "response.completed", map[string]any{"response": map[string]any{"service_tier": "default", "status": "completed"}})
		}))
		p := newOpenAIProvider("gpt-6-astra", "test", srv.URL, srv.Client(), 100, 0, "", "coddy")
		p.serviceTier = tier
		out, err := p.Complete(WithImageGeneration(context.Background()), []Message{{Role: RoleUser, Content: "draw"}}, nil)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if out.ServiceTier != "default" || len(out.GeneratedImages) != 1 {
			t.Fatalf("response=%+v", out)
		}
	}
}

func TestServiceTierChoices(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		want            []string
	}{
		{"openai", "gpt-6-astra", []string{"", "default", "priority", "ultrafast", "flex"}},
		{"codex", "gpt-6.1-sol", []string{"", "default", "priority", "ultrafast"}},
		{"codex", "gpt-6-luna", []string{"", "default", "priority"}},
		{"openai", "gpt-6-unknown", nil},
		{"anthropic", "gpt-6-astra", nil},
		{"openai", "gpt-5.6", nil},
	} {
		if got := ServiceTierChoices(tc.provider, tc.model); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s/%s: %v want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestServiceTierFailureKeepsRequest(t *testing.T) {
	for _, status := range []int{400, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests = append(requests, fmt.Sprint(body["service_tier"]))
				w.Header().Set("Content-Type", "application/json")
				if len(requests) == 1 {
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, `{"error":{"message":"test rejection","type":"invalid_request_error"}}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()
			p, err := NewProvider(ProviderInput{Type: "openai", Model: "gpt-6-astra", APIKey: "test", BaseURL: srv.URL, ProxyURL: "none", ServiceTier: ptrTier("priority"), RetryMax: 1, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
			if status == 400 {
				if err == nil || len(requests) != 1 {
					t.Fatalf("400: requests=%v err=%v", requests, err)
				}
			} else {
				if err != nil || len(requests) != 2 {
					t.Fatalf("429: requests=%v err=%v", requests, err)
				}
				if resp.ServiceTier != "" {
					t.Fatalf("invented response tier %q", resp.ServiceTier)
				}
			}
			for _, tier := range requests {
				if tier != "priority" {
					t.Fatalf("retry changed tier: %v", requests)
				}
			}
		})
	}
}
