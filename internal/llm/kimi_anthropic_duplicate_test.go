package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKimiAnthropicDuplicateToolIDs(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","id":"same","name":"first","input":{"secret":"private"}},{"type":"tool_use","id":"same","name":"second","input":{"x":2}}],"stop_reason":"tool_use"}`)
		}))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		r, err := p.Complete(context.Background(), nil, nil)
		if err == nil || (r != nil && len(r.ToolCalls) > 0) {
			t.Fatalf("duplicate accepted: response=%#v err=%v", r, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatalf("arguments leaked in error: %v", err)
		}
	})
	t.Run("stream", func(t *testing.T) {
		body := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"same\",\"name\":\"first\",\"input\":{\"secret\":\"private\"}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"same\",\"name\":\"second\",\"input\":{\"x\":2}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		var finals []ToolCall
		r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
			if c.ToolCall != nil {
				finals = append(finals, *c.ToolCall)
			}
		})
		if err == nil || len(finals) != 0 || (r != nil && len(r.ToolCalls) > 0) {
			t.Fatalf("duplicate accepted: response=%#v finals=%#v err=%v", r, finals, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatalf("arguments leaked in error: %v", err)
		}
	})
}

func TestKimiAnthropicDistinctToolStreamOrder(t *testing.T) {
	body := "data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"second\",\"name\":\"two\",\"input\":{\"x\":2}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"first\",\"name\":\"one\",\"input\":{\"x\":1}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	var finals []ToolCall
	r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			finals = append(finals, *c.ToolCall)
		}
	})
	if err != nil || r == nil || len(r.ToolCalls) != 2 || len(finals) != 2 || r.ToolCalls[0].ID != "first" || r.ToolCalls[0].InputJSON != `{"x":1}` || r.ToolCalls[1].ID != "second" || r.ToolCalls[1].InputJSON != `{"x":2}` || finals[0] != r.ToolCalls[0] || finals[1] != r.ToolCalls[1] {
		t.Fatalf("response=%#v finals=%#v err=%v", r, finals, err)
	}
}
