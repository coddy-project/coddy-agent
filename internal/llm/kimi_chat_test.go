package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKimiChatComplete(t *testing.T) {
	var request map[string]any
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing credential")
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok","reasoning_content":"thought"},"finish_reason":"stop","usage":{"prompt_tokens":3,"completion_tokens":2}}]}`))
	}))
	defer server.Close()
	tr := &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiChatProvider(tr, ProviderInput{Model: "kimi", MaxTokens: 100, ReasoningEffort: "high"})
	r, err := p.Complete(context.Background(), []Message{{Role: RoleAssistant, Reasoning: "earlier", ToolCalls: []ToolCall{{ID: "id", Name: "f", InputJSON: "{}"}}}, {Role: RoleTool, ToolCallID: "id", Content: "result"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || r.Content != "ok" || r.Reasoning != "thought" || !r.UsageKnown || r.InputTokens != 3 {
		t.Fatalf("response %#v calls %d", r, calls)
	}
	if request["max_completion_tokens"] != float64(100) || request["thinking"].(map[string]any)["effort"] != "high" {
		t.Fatalf("request %#v", request)
	}
	msgs := request["messages"].([]any)
	if msgs[0].(map[string]any)["reasoning_content"] != "earlier" {
		t.Fatal(msgs)
	}
}

func TestKimiChatStreamTruncatedFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[\n\n"))
	}))
	defer server.Close()
	tr := &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiChatProvider(tr, ProviderInput{Model: "kimi"})
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) || r == nil || r.Content != "partial" {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiChatStreamToolsAndUsage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing credential")
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"why\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"f\",\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\",\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":4}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	credentials := 0
	tr := &KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { credentials++; return "token", nil }}
	p := newKimiChatProvider(tr, ProviderInput{Model: "kimi"})
	for i := 0; i < 2; i++ {
		var chunks []StreamChunk
		r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) { chunks = append(chunks, c) })
		if err != nil || r.Reasoning != "why" || !r.UsageKnown || r.InputTokens != 7 || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != "{}" || r.StopReason != "tool_use" || len(chunks) < 3 {
			t.Fatalf("response=%#v chunks=%#v error=%v", r, chunks, err)
		}
	}
	if calls != 2 || credentials != 2 {
		t.Fatalf("calls=%d credentials=%d", calls, credentials)
	}
}
