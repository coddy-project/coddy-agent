package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOpenAIToolDeltasInterleaved(t *testing.T) {
	var s strings.Builder
	emit := func(index int, id, name, args string) {
		call := map[string]interface{}{"index": index, "function": map[string]string{"name": name, "arguments": args}}
		if id != "" {
			call["id"] = id
		}
		payload, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"index": 0, "delta": map[string]interface{}{"tool_calls": []interface{}{call}}}}})
		fmt.Fprintf(&s, "data: %s\n\n", payload)
	}
	emit(0, "a", "write", `{"content":"a`)
	emit(1, "b", "edit", `{"newString":"b`)
	emit(0, "", "", `b"}`)
	emit(1, "", "", `c"}`)
	s.WriteString("data: [DONE]\n\n")
	p, closeStub := streamStubProvider(t, s.String())
	defer closeStub()
	fragments := map[string]string{}
	completed := false
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		if d := c.ToolCallDelta; d != nil {
			if completed {
				t.Error("argument fragment arrived after completed call")
			}
			fragments[d.ID] += d.InputJSON
			if !chunkHasOutput(c) {
				t.Error("fragment must prevent fallback replay")
			}
		}
		if c.ToolCall != nil {
			completed = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if fragments["a"] != `{"content":"ab"}` || fragments["b"] != `{"newString":"bc"}` || len(resp.ToolCalls) != 2 {
		t.Fatalf("fragments=%v calls=%v", fragments, resp.ToolCalls)
	}
}

func TestAnthropicToolArgumentDeltas(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"test","usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"w","name":"write","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"content\":\"he"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"llo\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}`,
		`{"type":"message_stop"}`,
	}
	var s strings.Builder
	for _, e := range events {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(e), &obj); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&s, "event: %s\ndata: %s\n\n", obj["type"], e)
	}
	p, closeStub := anthropicStreamStub(t, s.String())
	defer closeStub()
	var args string
	named := false
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		if c.ToolCallNamed != nil {
			named = true
		}
		if c.ToolCallDelta != nil {
			if !named || c.ToolCallDelta.ID != "w" {
				t.Error("delta without matching announcement")
			}
			args += c.ToolCallDelta.InputJSON
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if args != `{"content":"hello"}` || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].InputJSON != args {
		t.Fatalf("args=%q response=%+v", args, resp)
	}
}
