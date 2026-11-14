package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKimiAnthropicCompleteAndReplay(t *testing.T) {
	var bodies []map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("headers %#v", r.Header)
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"thinking","thinking":"why","signature":"sig"},{"type":"text","text":"ok"},{"type":"tool_use","id":"id","name":"f","input":{"x":1}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":3,"cache_read_input_tokens":2}}`))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiAnthropicProvider(tr, ProviderInput{Model: "model", MaxTokens: 100, ReasoningEffort: "high"})
	r, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil)
	if err != nil || r == nil || r.Content != "ok" || r.Reasoning != "why" || r.ReasoningSignature == "" || len(r.ToolCalls) != 1 || !r.UsageKnown || r.InputTokens != 6 {
		t.Fatalf("response=%#v err=%v", r, err)
	}
	_, err = p.Complete(context.Background(), []Message{{Role: RoleAssistant, Content: r.Content, Reasoning: r.Reasoning, ReasoningSignature: r.ReasoningSignature, ToolCalls: r.ToolCalls}, {Role: RoleTool, ToolCallID: "id", Content: "done"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := bodies[0]
	if first["thinking"].(map[string]any)["type"] != "enabled" || first["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatal(first)
	}
	msgs := bodies[1]["messages"].([]any)
	blocks := msgs[0].(map[string]any)["content"].([]any)
	if blocks[0].(map[string]any)["signature"] != "sig" || msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)["tool_use_id"] != "id" {
		t.Fatal(msgs)
	}
}

func TestKimiAnthropicStreamTerminalAndCancel(t *testing.T) {
	cancelMode := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"))
		w.(http.Flusher).Flush()
		if cancelMode {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}
	p := newKimiAnthropicProvider(tr, ProviderInput{Model: "model"})
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err != nil || r == nil || r.Content != "partial" || !r.UsageKnown || r.InputTokens != 5 || r.OutputTokens != 2 {
		t.Fatalf("response=%#v err=%v", r, err)
	}
	cancelMode = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err = p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || r == nil || r.Content != "partial" {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiAnthropicThinkingOffAndHTTPError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if b["thinking"].(map[string]any)["type"] != "disabled" || b["output_config"] != nil {
			t.Errorf("body %#v", b)
		}
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}
	_, err := newKimiAnthropicProvider(tr, ProviderInput{Model: "m", ReasoningEffort: "off"}).Complete(context.Background(), nil, nil)
	if UpstreamStatus(err) != 429 {
		t.Fatalf("error %v", err)
	}
	if d, ok := UpstreamRetryAfter(err); !ok || d != 7*time.Second {
		t.Fatalf("retry %v %v", d, ok)
	}
}

func TestKimiAnthropicStreamToolAndReasoning(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"why\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"f\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer s.Close()
	tr := &KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}
	var chunks []StreamChunk
	r, err := newKimiAnthropicProvider(tr, ProviderInput{Model: "m"}).Stream(context.Background(), nil, nil, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil || r == nil || r.Reasoning != "why" || r.ReasoningSignature != "sig" || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != "{}" || len(chunks) < 3 || r.UsageKnown {
		t.Fatalf("response=%#v chunks=%#v err=%v", r, chunks, err)
	}
}

func TestKimiAnthropicUpstreamRegressions(t *testing.T) {
	t.Run("unsigned replay", func(t *testing.T) {
		var request map[string]any
		n := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			if n == 2 {
				_ = json.NewDecoder(r.Body).Decode(&request)
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"thinking","thinking":"unsigned"},{"type":"text","text":"answer"}],"stop_reason":"end_turn"}`))
		}))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m", ReasoningEffort: "on"})
		r, err := p.Complete(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Complete(context.Background(), []Message{{Role: RoleAssistant, Content: r.Content, Reasoning: r.Reasoning, ReasoningSignature: r.ReasoningSignature}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		msgs := request["messages"].([]any)
		blocks := msgs[0].(map[string]any)["content"].([]any)
		if blocks[0].(map[string]any)["type"] != "thinking" {
			t.Fatalf("unsigned reasoning lost: %#v", blocks)
		}
	})
	t.Run("default beta", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("anthropic-beta"); got != "interleaved-thinking-2025-05-14" {
				t.Errorf("beta %q", got)
			}
			_, _ = w.Write([]byte(`{"content":[],"stop_reason":"end_turn"}`))
		}))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		_, _ = p.Complete(context.Background(), nil, nil)
	})
	t.Run("initial blocks and usage", func(t *testing.T) {
		body := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"first\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"second\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"hi\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":7,\"cache_read_input_tokens\":3,\"output_tokens\":4}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
		if err != nil || r == nil || r.Content != "hi" || r.Reasoning != "first\n\nsecond" || r.InputTokens != 10 {
			t.Fatalf("result %#v error %v", r, err)
		}
	})
	t.Run("unknown tool delta", func(t *testing.T) {
		body := "data: {\"type\":\"content_block_delta\",\"index\":9,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		defer s.Close()
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		_, err := p.Stream(context.Background(), nil, nil, nil)
		if err == nil {
			t.Fatal("accepted invalid sequence")
		}
	})
}

func TestKimiAnthropicStreamErrorAndUnfinishedTool(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"in-band overload", "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n", 529},
		{"truncated tool", "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"f\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer s.Close()
			p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
			r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
			if err == nil || UpstreamStatus(err) != tc.status || (r != nil && len(r.ToolCalls) > 0) {
				t.Fatalf("response=%#v error=%v status=%d", r, err, UpstreamStatus(err))
			}
		})
	}
}

func TestKimiAnthropicUnsignedReplayUsesCurrentMessage(t *testing.T) {
	sig := kimiAnthropicSignature([]anthropicReplayBlock{{Type: anthropicBlockThinking, Thinking: "think"}, {Type: anthropicBlockText, Text: "old"}})
	b, err := json.Marshal(kimiAnthropicAssistant(Message{Content: "new", Reasoning: "think", ReasoningSignature: sig, ToolCalls: []ToolCall{{ID: "c", Name: "f", InputJSON: "{}"}}}, true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"text":"new"`) || !strings.Contains(string(b), `"id":"c"`) || strings.Contains(string(b), `"text":"old"`) {
		t.Fatalf("stale replay %s", b)
	}
}

func TestKimiAnthropicRejectsMalformedToolInput(t *testing.T) {
	data := "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"f\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(data)) }))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err == nil || (r != nil && len(r.ToolCalls) > 0) {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiAnthropicToolBlockValidation(t *testing.T) {
	for _, tc := range []struct {
		name, delta string
		valid       bool
	}{
		{"initial input", "", true},
		{"wrong delta", "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"bad\"}}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c\",\"name\":\"f\",\"input\":{\"x\":1}}}\n\n" + tc.delta + "data: {\"type\":\"content_block_stop\",\"index\":1}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
			r, err := p.Stream(context.Background(), nil, nil, nil)
			if tc.valid {
				if err != nil || r == nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != `{"x":1}` {
					t.Fatalf("result %#v err %v", r, err)
				}
			} else if err == nil || (r != nil && len(r.ToolCalls) > 0) {
				t.Fatalf("accepted result %#v err %v", r, err)
			}
		})
	}
}

func TestKimiAnthropicUsageDeltaPreservesMissingCounters(t *testing.T) {
	data := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2,\"cache_read_input_tokens\":3}}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":7,\"output_tokens\":4}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(data)) }))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	r, err := p.Stream(context.Background(), nil, nil, nil)
	if err != nil || r == nil || r.InputTokens != 10 || r.CachedInputTokens != 3 {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiAnthropicRejectsInvalidBlockSequence(t *testing.T) {
	for _, middle := range []string{"", "data: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"} {
		data := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"partial\"}}\n\n" + middle + "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(data)) }))
		p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
		r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
		s.Close()
		if err == nil || r == nil || r.Content != "partial" {
			t.Fatalf("response=%#v err=%v", r, err)
		}
	}
}

func TestKimiAnthropicCompleteRejectsInvalidTool(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"c","name":"f","input":"not-object"}],"stop_reason":"tool_use"}`))
	}))
	defer s.Close()
	p := newKimiAnthropicProvider(&KimiInferenceTransport{endpoint: s.URL, client: s.Client(), anthropic: true, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "m"})
	r, err := p.Complete(context.Background(), nil, nil)
	if err == nil || (r != nil && len(r.ToolCalls) > 0) {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}
