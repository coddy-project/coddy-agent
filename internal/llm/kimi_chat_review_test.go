package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func kimiChatStreamFixture(t *testing.T, data string) *kimiChatProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, data) }))
	t.Cleanup(server.Close)
	return newKimiChatProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "kimi"})
}

func TestKimiChatSparseToolIndices(t *testing.T) {
	p := kimiChatStreamFixture(t, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":3,\"id\":\"c\",\"function\":{\"name\":\"f\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err != nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "c" {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}

func TestKimiChatToolArgumentsBeforeName(t *testing.T) {
	p := kimiChatStreamFixture(t, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"f\",\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	var delta strings.Builder
	_, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCallDelta != nil {
			delta.WriteString(c.ToolCallDelta.InputJSON)
		}
	})
	if err != nil || delta.String() != "{}" {
		t.Fatalf("delta=%q err=%v", delta.String(), err)
	}
}

func TestKimiChatCancelledPartialTool(t *testing.T) {
	p := kimiChatStreamFixture(t, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"f\",\"arguments\":\"{\"}}]}}]}\n\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable := false
	r, err := p.Stream(ctx, nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			executable = true
		}
		if c.ToolCallNamed != nil {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || r == nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != "{" || executable {
		t.Fatalf("response=%#v err=%v executable=%v", r, err, executable)
	}
}

func TestKimiChatNormalizesToolSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		schema := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
		prop := schema["properties"].(map[string]any)["name"].(map[string]any)
		if prop["type"] != "string" || prop["$ref"] != nil || schema["$defs"] != nil {
			t.Errorf("schema=%#v", schema)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	p := newKimiChatProvider(&KimiInferenceTransport{endpoint: server.URL, client: server.Client(), source: func(context.Context) (string, error) { return "token", nil }}, ProviderInput{Model: "kimi"})
	_, err := p.Complete(context.Background(), nil, []ToolDefinition{{Name: "f", InputSchema: map[string]any{"type": "object", "$defs": map[string]any{"s": map[string]any{"enum": []any{"x"}}}, "properties": map[string]any{"name": map[string]any{"$ref": "#/$defs/s"}}}}})
	if err != nil {
		t.Fatal(err)
	}
}
