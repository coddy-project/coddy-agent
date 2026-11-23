package llm

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestKimi776StreamValidation(t *testing.T) {
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

func TestKimi776CutLastAnnounced(t *testing.T) {
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

func TestKimi776FragmentEveryByte(t *testing.T) {
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

func TestKimi776MalformedSiblingWithholdsAll(t *testing.T) {
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

func TestKimi776LateName(t *testing.T) {
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

func TestKimi776RawMaxTokens(t *testing.T) {
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
