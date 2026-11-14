package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestKimiChatSchemaRejectsNonObjectReference(t *testing.T) {
	for _, v := range []any{[]any{"secret-value"}, "secret-value", false, float64(42)} {
		t.Run(fmt.Sprintf("%T", v), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("unexpected panic: %v", r)
				}
			}()
			_, err := kimiChatNormalizeSchema(map[string]any{"$ref": "#/$defs/x", "$defs": map[string]any{"x": v}})
			if err == nil || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("expected safe error, got %v", err)
			}
		})
	}
}

func TestKimiChatSchemaUnconstrainedProperties(t *testing.T) {
	schema, err := kimiChatNormalizeSchema(map[string]any{"type": "object", "properties": map[string]any{"any": map[string]any{}, "described": map[string]any{"description": "anything"}}})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range schema["properties"].(map[string]any) {
		if _, ok := v.(map[string]any)["type"]; ok {
			t.Errorf("%s restricted: %#v", k, v)
		}
	}
}

func TestKimiChatStreamRejectsInvalidFinalTools(t *testing.T) {
	cases := []struct{ name, id, tool, args string }{{"missing-id", "", "f", "{}"}, {"blank-id", " ", "f", "{}"}, {"missing-name", "id", "", "{}"}, {"invalid-json", "id", "f", "{"}, {"array", "id", "f", "[]"}, {"null", "id", "f", "null"}, {"string", "id", "f", `"x"`}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "partial", "tool_calls": []any{map[string]any{"index": 0, "id": tc.id, "function": map[string]any{"name": tc.tool, "arguments": tc.args}}}}, "finish_reason": "tool_calls"}}})
			p := kimiChatStreamFixture(t, "data: "+string(frame)+"\n\ndata: [DONE]\n\n")
			executable := false
			r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
				if c.ToolCall != nil {
					executable = true
				}
			})
			if err == nil || executable || r == nil || r.Content != "partial" || len(r.ToolCalls) != 0 || isRetryableLLMError(err) {
				t.Fatalf("response=%#v err=%v executable=%v", r, err, executable)
			}
		})
	}
}
