package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	cases := []struct{ name, id, tool, args string }{{"missing-name", "id", "", "{}"}, {"invalid-json", "id", "f", "{"}, {"array", "id", "f", "[]"}, {"null", "id", "f", "null"}, {"string", "id", "f", `"x"`}}
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

func TestKimiChatStreamValidatesTerminalToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name, args, finish string
		cut, valid         bool
	}{
		{"malformed", `{"password":"secret`, "tool_calls", false, false},
		{"cut-invalid", `{`, "length", true, true},
		{"cut-valid", `{}`, "length", true, true},
		{"missing-id", `{}`, "tool_calls", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"write","arguments":` + strconv.Quote(tc.args) + `}}]},"finish_reason":"` + tc.finish + `"}]}` + "\n\n"
			p := kimiChatStreamFixture(t, frame)
			var named, executed string
			r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
				if c.ToolCallNamed != nil {
					named = c.ToolCallNamed.ID
				}
				if c.ToolCall != nil {
					executed = c.ToolCall.ID
				}
			})
			if !tc.valid {
				var e *StreamedToolCallError
				if !errors.As(err, &e) || strings.Contains(err.Error(), "secret") || isRetryableLLMError(err) {
					t.Fatalf("unsafe or retryable error: %v", err)
				}
				return
			}
			if err != nil || r == nil || named == "" {
				t.Fatalf("missing announced call: response=%+v err=%v", r, err)
			}
			if tc.cut && (len(r.ToolCalls) != 0 || r.CutToolCall == nil || r.CutToolCall.ID != named) || !tc.cut && (r.CutToolCall != nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != named) {
				t.Fatalf("response=%+v err=%v named=%q", r, err, named)
			}
			if (executed != "") == tc.cut {
				t.Fatalf("cut=%v executed=%q", tc.cut, executed)
			}
		})
	}
}

func TestKimiChatStreamWithholdsLastAnnouncedCallOnLength(t *testing.T) {
	p := kimiChatStreamFixture(t, `data: {"choices":[{"delta":{"tool_calls":[{"index":8,"id":"earlier","function":{"name":"read","arguments":"{}"}},{"index":1,"id":"last","function":{"name":"write","arguments":"{}"}}]},"finish_reason":"length"}]}`+"\n\n")
	var executed []string
	r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			executed = append(executed, c.ToolCall.ID)
		}
	})
	if err != nil || r.StopReason != "max_tokens" || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "earlier" || r.CutToolCall == nil || r.CutToolCall.ID != "last" || len(executed) != 1 || executed[0] != "earlier" {
		t.Fatalf("response=%+v err=%v executed=%v", r, err, executed)
	}
}

func TestKimiChatStreamReassemblesArgumentsAcrossEveryByteSplit(t *testing.T) {
	args := `{"text":"some content"}`
	for i := 0; i <= len(args); i++ {
		frame := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"id","function":{"name":"write","arguments":` + strconv.Quote(args[:i]) + `}}]}}]}` + "\n\n" + `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + strconv.Quote(args[i:]) + `}}]},"finish_reason":"tool_calls"}]}` + "\n\n"
		p := kimiChatStreamFixture(t, frame)
		var progress strings.Builder
		r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
			if c.ToolCallDelta != nil {
				progress.WriteString(c.ToolCallDelta.InputJSON)
			}
		})
		if err != nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].InputJSON != args || progress.String() != args {
			t.Fatalf("split=%d response=%+v err=%v progress=%q", i, r, err, progress.String())
		}
	}
}

func TestKimiChatStreamRejectsBatchWithMalformedSibling(t *testing.T) {
	p := kimiChatStreamFixture(t, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"good","function":{"name":"read","arguments":"{}"}},{"index":3,"id":"bad","function":{"name":"write","arguments":"[\"secret-token\"]"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
	var executed bool
	_, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			executed = true
		}
	})
	var diagnostic *StreamedToolCallError
	if !errors.As(err, &diagnostic) || executed || diagnostic.Index != 3 || diagnostic.JSONKind != "array" || diagnostic.FinishReason != "tool_calls" || strings.Contains(err.Error(), "secret-token") || isRetryableLLMError(err) {
		t.Fatalf("err=%v diagnostic=%+v executed=%v", err, diagnostic, executed)
	}
}

func TestKimiChatStreamUsesAnnouncementOrderForLateToolName(t *testing.T) {
	p := kimiChatStreamFixture(t, `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":""}},{"index":8,"id":"earlier","function":{"name":"read","arguments":"{}"}},{"index":1,"id":"last","function":{"name":"write","arguments":"{}"}}]},"finish_reason":"length"}]}`+"\n\n")
	var executed []string
	r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			executed = append(executed, c.ToolCall.ID)
		}
	})
	if err != nil || r.StopReason != "max_tokens" || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "earlier" || r.CutToolCall == nil || r.CutToolCall.ID != "last" || len(executed) != 1 || executed[0] != "earlier" {
		t.Fatalf("response=%+v err=%v executed=%v", r, err, executed)
	}
}

func TestKimiChatStreamWithholdsLastCallOnMaxTokens(t *testing.T) {
	p := kimiChatStreamFixture(t, `data: {"choices":[{"delta":{"tool_calls":[{"index":8,"id":"earlier","function":{"name":"read","arguments":"{}"}},{"index":1,"id":"last","function":{"name":"write","arguments":"{}"}}]},"finish_reason":"max_tokens"}]}`+"\n\n")
	var executed []string
	r, err := p.Stream(context.Background(), nil, nil, func(c StreamChunk) {
		if c.ToolCall != nil {
			executed = append(executed, c.ToolCall.ID)
		}
	})
	if err != nil || r.StopReason != "max_tokens" || len(r.ToolCalls) != 1 || r.ToolCalls[0].ID != "earlier" || r.CutToolCall == nil || r.CutToolCall.ID != "last" || len(executed) != 1 || executed[0] != "earlier" {
		t.Fatalf("response=%+v err=%v executed=%v", r, err, executed)
	}
}
