package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestKimiResponsesRejectInvalidToolCalls(t *testing.T) {
	cases := []struct{ name, id, tool, args string }{
		{"missing-id", "", "f", "{}"}, {"blank-id", " ", "f", "{}"}, {"missing-name", "c", "", "{}"}, {"blank-name", "c", " ", "{}"},
		{"invalid-json", "c", "f", "{"}, {"array", "c", "f", "[]"}, {"null", "c", "f", "null"}, {"string", "c", "f", `"x"`},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				good := map[string]any{"type": "function_call", "id": "i1", "call_id": "good", "name": "f", "arguments": "{}"}
				bad := map[string]any{"type": "function_call", "id": "i2", "call_id": tc.id, "name": tc.tool, "arguments": tc.args}
				var wire string
				if stream {
					wire = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
					for _, item := range []map[string]any{good, bad} {
						added := map[string]any{"type": "response.output_item.added", "item": item}
						done := map[string]any{"type": "response.output_item.done", "item": item}
						for _, event := range []any{added, done} {
							b, _ := json.Marshal(event)
							wire += "data: " + string(b) + "\n\n"
						}
					}
					wire += "data: {\"type\":\"response.completed\"}\n\n"
				} else {
					b, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "partial"}}}, good, bad}})
					wire = string(b)
				}
				p := kimiResponsesFixture(t, wire)
				var r *Response
				var err error
				published := false
				if stream {
					r, err = p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
						if c.ToolCall != nil {
							published = true
						}
					})
				} else {
					r, err = p.Complete(context.Background(), nil, nil)
				}
				if err == nil || published || (r != nil && len(r.ToolCalls) != 0) || (stream && (r == nil || r.Content != "partial" || isRetryableLLMError(err))) {
					t.Fatalf("response=%#v err=%v published=%v", r, err, published)
				}
			})
		}
	}
}

func TestKimiResponsesErrorNotOverwrittenByCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		server      bool
	}{
		{"server", `{"type":"error","error":{"code":503,"message":"busy"}}`, true},
		{"decode", `{"type":!}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wire := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: " + tc.frame + "\n\n"
			client := &http.Client{Transport: kimiResponsesRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(wire))}, nil
			})}
			p := newKimiResponsesProvider(&KimiInferenceTransport{endpoint: "https://example.test", client: client, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "model"})
			r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
				if c.TextDelta != "" {
					cancel()
				}
			})
			var se *streamServerError
			var de *streamUndecodableError
			if err == nil || errors.Is(err, context.Canceled) || (tc.server && !errors.As(err, &se)) || (!tc.server && !errors.As(err, &de)) || isRetryableLLMError(err) || r == nil || r.Content != "partial" {
				t.Fatalf("response=%#v err=%v", r, err)
			}
		})
	}
}
