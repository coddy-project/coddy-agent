package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKimiAnthropicReviewInvalidTools(t *testing.T) {
	for _, tc := range []struct{ name, id, tool, args string }{
		{"empty id", "", "f", "{}"}, {"empty name", "c", "", "{}"}, {"malformed", "c", "f", "{"}, {"nonobject", "c", "f", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": tc.id, "name": tc.tool}})
			delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": tc.args}})
			body := "data: " + string(start) + "\n\ndata: " + string(delta) + "\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer s.Close()
			p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
			var chunks []StreamChunk
			r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) { chunks = append(chunks, c) })
			if err == nil || (r != nil && len(r.ToolCalls) != 0) {
				t.Fatalf("invalid call returned: %#v, %v", r, err)
			}
			for _, c := range chunks {
				if c.ToolCall != nil || ((tc.id == "" || tc.tool == "") && c.ToolCallNamed != nil) {
					t.Fatalf("invalid call emitted: %#v", c)
				}
			}
		})
	}
}

func TestKimiAnthropicReviewCancelDeclaredTool(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"f\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var final bool
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			final = true
		}
		if c.ToolCallNamed != nil {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || r == nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "c" || final {
		t.Fatalf("response=%#v err=%v final=%v", r, err, final)
	}
}

func TestKimiAnthropicReviewLateCancelAfterTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"done\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	client := &http.Client{Transport: kimiRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: "https://example.invalid", client: client, anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	})
	if err != nil || r == nil || r.Content != "done" {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiAnthropicReviewSystemMessageOrder(t *testing.T) {
	var body map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"content":[],"stop_reason":"end_turn"}`)
	}))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	_, err := p.Complete(context.Background(), []Message{{Role: RoleSystem, Content: "first"}, {Role: RoleSystem, Content: "second"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	system := body["system"].([]any)
	if len(system) != 2 || system[0].(map[string]any)["text"] != "first" || system[1].(map[string]any)["text"] != "second" {
		t.Fatalf("system=%#v", system)
	}
}
