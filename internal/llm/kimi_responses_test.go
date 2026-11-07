package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKimiResponsesCompleteReplay(t *testing.T) {
	var requests []map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"reasoning","id":"r","summary":[{"type":"summary_text","text":"why"}],"encrypted_content":"opaque"},{"type":"message","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}],"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}`))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiResponsesProvider(tr, ProviderInput{Model: "model", MaxTokens: 100, ReasoningEffort: "high"})
	r, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil)
	if err != nil || r == nil {
		t.Fatalf("response=%#v err=%v", r, err)
	}
	if r.Content != "ok" || r.Reasoning != "why" || len(r.ToolCalls) != 1 || !r.UsageKnown || r.InputTokens != 7 || r.CachedInputTokens != 2 {
		t.Fatalf("response=%#v", r)
	}
	_, err = p.Complete(context.Background(), []Message{{Role: RoleAssistant, Content: r.Content, ReasoningSignature: r.ReasoningSignature, ToolCalls: r.ToolCalls}, {Role: RoleTool, ToolCallID: "c", Content: "result"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(requests[1])
	if !strings.Contains(string(b), `"encrypted_content":"opaque"`) || !strings.Contains(string(b), `"function_call_output"`) {
		t.Fatal(string(b))
	}
	if requests[0]["stream"] != false || requests[0]["max_output_tokens"] != float64(100) {
		t.Fatal(requests[0])
	}
}

func TestKimiResponsesStreamAndTruncation(t *testing.T) {
	truncated := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
		if truncated {
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"i\",\"call_id\":\"c\",\"name\":\"f\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"i\",\"delta\":\"{}\"}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"id\":\"i\",\"call_id\":\"c\",\"name\":\"f\",\"arguments\":\"{}\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiResponsesProvider(tr, ProviderInput{Model: "model"})
	var chunks []StreamChunk
	r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil || r == nil || r.Content != "hello" || len(r.ToolCalls) != 1 || !r.UsageKnown || r.InputTokens != 4 || len(chunks) < 3 {
		t.Fatalf("response=%#v chunks=%#v err=%v", r, chunks, err)
	}
	truncated = true
	r, err = p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) || r == nil || r.Content != "hello" || len(r.ToolCalls) != 0 {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiResponsesCancelKeepsPartialAndUnknownUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thought\"}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiResponsesProvider(tr, ProviderInput{Model: "model"})
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.ReasoningDelta != "" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || r == nil || r.Reasoning != "thought" || r.UsageKnown || !r.UsageIncomplete {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiResponsesRejectsUnfinishedTool(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"i\",\"call_id\":\"c\",\"name\":\"f\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), source: func(context.Context) (string, error) { return "token", nil }}
	r, err := newKimiResponsesProvider(tr, ProviderInput{Model: "model"}).Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err == nil || (r != nil && len(r.ToolCalls) != 0) {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}
