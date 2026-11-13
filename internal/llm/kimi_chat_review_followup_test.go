package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKimiChatCompleteRejectsInvalidTools(t *testing.T) {
	for _, tc := range []struct{ name, id, tool, args string }{
		{"missing-id", "", "f", "{}"}, {"blank-id", " ", "f", "{}"}, {"missing-name", "id", "", "{}"}, {"blank-name", "id", " ", "{}"}, {"invalid-json", "id", "f", "secret{"}, {"array", "id", "f", "[]"}, {"null", "id", "f", "null"}, {"string", "id", "f", `"secret"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "answer", "tool_calls": []any{map[string]any{"id": "valid", "type": "function", "function": map[string]any{"name": "good", "arguments": "{}"}}, map[string]any{"id": tc.id, "type": "function", "function": map[string]any{"name": tc.tool, "arguments": tc.args}}}}, "finish_reason": "tool_calls"}}})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
			defer server.Close()
			p := newKimiChatProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "kimi"})
			r, err := p.Complete(context.Background(), nil, nil)
			if err == nil || (r != nil && len(r.ToolCalls) > 0) || strings.Contains(fmt.Sprint(err), "secret") {
				t.Fatalf("response=%#v error=%v", r, err)
			}
		})
	}
}

type kimiCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *kimiCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestKimiChatStreamErrorPrecedesCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		server      bool
	}{
		{"server", `data: {"error":{"code":503,"message":"offline"}}` + "\n\n", true},
		{"malformed", `data: {not-json}` + "\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: kimiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&kimiCancelReader{Reader: strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" + tc.frame), cancel: cancel}), Request: r}, nil
			})}
			p := newKimiChatProvider(&KimiInferenceTransport{endpoint: "https://example.test", client: client, source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "kimi"})
			r, err := p.Stream(ctx, nil, nil, func(StreamChunk) {})
			var serverErr *streamServerError
			var decodeErr *streamUndecodableError
			if err == nil || errors.Is(err, context.Canceled) || (tc.server && !errors.As(err, &serverErr)) || (!tc.server && !errors.As(err, &decodeErr)) || isRetryableLLMError(err) || r == nil || r.Content != "partial" {
				t.Fatalf("response=%#v error=%v", r, err)
			}
		})
	}
}

func TestKimiChatDoneWithoutFinishReason(t *testing.T) {
	p := kimiChatStreamFixture(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err != nil || r == nil || r.Content != "ok" || r.StopReason != "end_turn" {
		t.Fatalf("response=%#v error=%v", r, err)
	}
}

func TestKimiChatCompleteValidTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"id","type":"function","function":{"name":"f","arguments":"{\"n\":1}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()
	p := newKimiChatProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "kimi"})
	r, err := p.Complete(context.Background(), nil, nil)
	if err != nil || r == nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != `{"n":1}` {
		t.Fatalf("response=%#v error=%v", r, err)
	}
}
