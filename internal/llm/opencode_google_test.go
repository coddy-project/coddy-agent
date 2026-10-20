package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const opencodeGoogleAnswer = `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"thoughtsTokenCount":3,"cachedContentTokenCount":4}}`

func TestOpenCodeGoogleToolConversation(t *testing.T) {
	bodies := make(chan map[string]any, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference/go/google/v1beta/models/gemini-4-argon-contributor:generateContent" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("x-opencode-session") != "sess_google" || r.Header.Get("User-Agent") != opencodeUserAgent() || r.Header.Get("Authorization") != "Bearer oc-key" {
			t.Error("missing gateway headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		if count.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"checking","thought":true},{"functionCall":{"name":"read","args":{"path":"a.go"}},"thoughtSignature":"signed-call"}]},"finishReason":"STOP"}]}`)
		} else {
			_, _ = io.WriteString(w, opencodeGoogleAnswer)
		}
	}))
	defer server.Close()
	t.Setenv(EnvOpenCodeBaseURL, server.URL+"/inference/go/v1")
	p, err := NewProvider(ProviderInput{Type: "opencode", Model: "gemini-4-argon-contributor", APIKey: "oc-key", SessionID: "sess_google", ProxyURL: "none", RetryDisabled: true, MaxTokens: 200, TemperatureSet: true})
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{{Role: RoleSystem, Content: "coding agent"}, {Role: RoleUser, Content: "read a.go", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,aGVsbG8="}}}}
	tools := []ToolDefinition{{Name: "read", InputSchema: map[string]any{"type": "object"}}}
	first, err := p.Complete(context.Background(), messages, tools)
	if err != nil {
		t.Fatal(err)
	}
	if first.StopReason != "tool_use" || len(first.ToolCalls) != 1 || first.Reasoning != "checking" {
		t.Fatalf("tool response: %+v", first)
	}
	messages = append(messages, Message{Role: RoleAssistant, ToolCalls: first.ToolCalls, Reasoning: first.Reasoning, ReasoningSignature: first.ReasoningSignature}, Message{Role: RoleTool, ToolCallID: first.ToolCalls[0].ID, Content: "package main"})
	final, err := p.Complete(context.Background(), messages, tools)
	if err != nil {
		t.Fatal(err)
	}
	if final.Content != "pong" || final.InputTokens != 8 || final.OutputTokens != 5 || final.CachedInputTokens != 4 {
		t.Fatalf("final response: %+v", final)
	}
	<-bodies
	encoded, _ := json.Marshal(<-bodies)
	for _, want := range []string{`"thoughtSignature":"signed-call"`, `"functionResponse":`, `"name":"read"`, `"output":"package main"`, `"inlineData":`, `"systemInstruction":`, `"parametersJsonSchema":`, `"temperature":0`, `"maxOutputTokens":200`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("request missing %s: %s", want, encoded)
		}
	}
}

func TestOpenCodeGoogleStream(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		truncated  bool
		content    string
	}{
		{"complete", ": keepalive\n\ndata: " + opencodeGoogleAnswer + "\n\n", false, "pong"},
		{"cut", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"},{\"functionCall\":{\"name\":\"read\",\"args\":{}}}]}}]}\n\n", true, "partial"},
		{"bad-json", "data: broken-json\n\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/inference/google/v1beta/models/gemini-4-argon-contributor:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
					t.Errorf("URL: %s", r.URL)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.wire)
			}))
			defer server.Close()
			t.Setenv(EnvOpenCodeBaseURL, server.URL+"/inference/v1")
			p, err := NewProvider(ProviderInput{Type: "opencode", Model: "gemini-4-argon-contributor", APIKey: "oc-key", ProxyURL: "none", RetryDisabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var text string
			resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil, func(c StreamChunk) { text += c.TextDelta })
			if text != tc.content {
				t.Fatalf("streamed %q, want %q", text, tc.content)
			}
			if tc.name == "complete" && (err != nil || resp.Content != "pong") {
				t.Fatalf("complete: %+v, %v", resp, err)
			}
			if tc.name != "complete" && err == nil {
				t.Fatal("expected stream failure")
			}
			if tc.truncated && (!IsStreamTruncated(err) || resp.Content != "partial" || len(resp.ToolCalls) != 0) {
				t.Fatalf("partial: %+v, %v", resp, err)
			}
			if tc.truncated && isRetryableLLMError(err) {
				t.Fatal("must not replay emitted deltas")
			}
		})
	}
}

func TestOpenCodeGoogleCarrierIsolation(t *testing.T) {
	p := &opencodeGoogleProvider{input: ProviderInput{Model: "gemini-4-argon-contributor"}}
	for _, sig := range []string{`{"opencode_google_model":"another-model","parts":[{"text":"secret"}]}`, `{"opencode":true,"items":[{}]}`} {
		body, err := p.requestBody([]Message{{Role: RoleAssistant, Content: "answer", ReasoningSignature: sig}}, nil)
		if err != nil || strings.Contains(string(body), "secret") || !strings.Contains(string(body), "answer") {
			t.Fatalf("body: %s, err: %v", body, err)
		}
	}
}

// A refusal of the Google path is classified the way the SDK errors of the
// other API families are: UpstreamStatus names the status, a 5xx is retried
// while nothing streamed, and a 429's Retry-After is the pause the wrapper
// reports as the reset of a usage limit when it would not wait that long.
func TestOpenCodeGoogleHTTPErrorsClassify(t *testing.T) {
	for _, tc := range []struct {
		status     int
		retryAfter string
		retryable  bool
		reset      time.Duration
	}{
		{http.StatusTooManyRequests, "7", false, 7 * time.Second},
		{http.StatusServiceUnavailable, "", true, 0},
		{http.StatusBadRequest, "", false, 0},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"code":`+strconv.Itoa(tc.status)+`,"message":"refused","status":"REFUSED"}}`)
			}))
			defer server.Close()
			t.Setenv(EnvOpenCodeBaseURL, server.URL+"/inference/go/v1")
			p, err := NewProvider(ProviderInput{Type: "opencode", Model: "gemini-4-argon-contributor", APIKey: "oc-key", ProxyURL: "none", RetryDisabled: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, streaming := range []bool{false, true} {
				var err error
				if streaming {
					_, err = p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil, func(StreamChunk) {})
				} else {
					_, err = p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
				}
				if err == nil {
					t.Fatalf("streaming=%v: expected an error", streaming)
				}
				if got := UpstreamStatus(err); got != tc.status {
					t.Errorf("streaming=%v: UpstreamStatus = %d, want %d (%v)", streaming, got, tc.status, err)
				}
				if got := isRetryableLLMError(err); got != tc.retryable {
					t.Errorf("streaming=%v: retryable = %v, want %v (%v)", streaming, got, tc.retryable, err)
				}
				var reset *QuotaResetError
				if tc.reset > 0 && (!errors.As(err, &reset) || reset.Delay != tc.reset) {
					t.Errorf("streaming=%v: want a usage-limit reset in %v, got %v", streaming, tc.reset, err)
				}
			}
		})
	}
}
