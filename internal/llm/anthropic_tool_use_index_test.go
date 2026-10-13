package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The Messages API numbers the content blocks of one answer from zero, text and
// thinking blocks included, so the index of a tool_use block is its position in
// the whole answer, not among the tool calls. These tests stream real SSE
// through the Anthropic SDK client and hold the reader to that: a tool_use
// behind any other block is a call like any other.

// anthropicSSE frames events the way the Messages API streams them.
func anthropicSSE(t *testing.T, events ...string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range events {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(e), &head); err != nil {
			t.Fatalf("event %s: %v", e, err)
		}
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", head.Type, e)
	}
	return b.String()
}

func anthropicMessageStart() string {
	return `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"test-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`
}

func anthropicMessageEnd(stopReason string) []string {
	return []string{
		`{"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_sequence":null},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
}

func anthropicTextBlock(index int, text string) []string {
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%q}}`, index, text),
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index),
	}
}

func anthropicThinkingBlock(index int, thinking, signature string) []string {
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":"","signature":""}}`, index),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%q}}`, index, thinking),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":%q}}`, index, signature),
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index),
	}
}

// anthropicToolUseStart opens a client tool_use block and streams its input
// as the given JSON fragments, without closing the block.
func anthropicToolUseStart(index int, id, name string, fragments ...string) []string {
	events := []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, index, id, name),
	}
	for _, f := range fragments {
		events = append(events, fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, index, f))
	}
	return events
}

func anthropicToolUseBlock(index int, id, name string, fragments ...string) []string {
	return append(anthropicToolUseStart(index, id, name, fragments...), fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}

// anthropicServerToolBlock is a block of a type the reader does not collect
// (a server-side tool call): it occupies an index and nothing else.
func anthropicServerToolBlock(index int, id string) []string {
	return []string{
		fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"server_tool_use","id":%q,"name":"web_search","input":{}}}`, index, id),
		fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, index, `{"query":"x"}`),
		fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index),
	}
}

func anthropicEvents(parts ...[]string) []string {
	out := []string{anthropicMessageStart()}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// streamedCalls is what a caller of Stream sees of the tool calls: the order
// in which they were announced, the argument fragments per call, and the
// complete calls delivered as chunks.
type streamedCalls struct {
	announced []string
	fragments map[string]string
	completed []ToolCall
}

func collectToolCalls() (*streamedCalls, func(StreamChunk)) {
	s := &streamedCalls{fragments: map[string]string{}}
	return s, func(c StreamChunk) {
		if c.ToolCallNamed != nil {
			s.announced = append(s.announced, c.ToolCallNamed.ID)
		}
		if c.ToolCallDelta != nil {
			s.fragments[c.ToolCallDelta.ID] += c.ToolCallDelta.InputJSON
		}
		if c.ToolCall != nil {
			s.completed = append(s.completed, *c.ToolCall)
		}
	}
}

func TestAnthropicStreamToolUseBehindOtherBlocks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		blocks    [][]string
		wantCalls []ToolCall
		wantText  string
		wantThink string
	}{
		{
			name:      "after text",
			blocks:    [][]string{anthropicTextBlock(0, "Let me look."), anthropicToolUseBlock(1, "toolu_a", "read", `{"path":`, `"a.go"}`)},
			wantCalls: []ToolCall{{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`}},
			wantText:  "Let me look.",
		},
		{
			name:      "after thinking",
			blocks:    [][]string{anthropicThinkingBlock(0, "I should read it.", "sig"), anthropicToolUseBlock(1, "toolu_a", "read", `{"path":"a.go"}`)},
			wantCalls: []ToolCall{{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`}},
			wantThink: "I should read it.",
		},
		{
			name: "after thinking and text",
			blocks: [][]string{
				anthropicThinkingBlock(0, "Hmm.", "sig"),
				anthropicTextBlock(1, "Reading."),
				anthropicToolUseBlock(2, "toolu_a", "read", `{"path":"a.go"}`),
			},
			wantCalls: []ToolCall{{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`}},
			wantText:  "Reading.",
			wantThink: "Hmm.",
		},
		{
			name: "several calls keep the order the model wrote them",
			blocks: [][]string{
				anthropicTextBlock(0, "Both."),
				anthropicToolUseBlock(1, "toolu_a", "read", `{"path":"a.go"}`),
				anthropicToolUseBlock(2, "toolu_b", "glob", `{"pattern":"*.go"}`),
				anthropicToolUseBlock(3, "toolu_c", "read", `{"path":"c.go"}`),
			},
			wantCalls: []ToolCall{
				{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`},
				{ID: "toolu_b", Name: "glob", InputJSON: `{"pattern":"*.go"}`},
				{ID: "toolu_c", Name: "read", InputJSON: `{"path":"c.go"}`},
			},
			wantText: "Both.",
		},
		{
			name: "calls separated by blocks the reader does not collect",
			blocks: [][]string{
				anthropicTextBlock(0, "Searching."),
				anthropicServerToolBlock(1, "srvtoolu_1"),
				anthropicToolUseBlock(3, "toolu_a", "read", `{"path":"a.go"}`),
				anthropicServerToolBlock(4, "srvtoolu_2"),
				anthropicToolUseBlock(7, "toolu_b", "read", `{"path":"b.go"}`),
			},
			wantCalls: []ToolCall{
				{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`},
				{ID: "toolu_b", Name: "read", InputJSON: `{"path":"b.go"}`},
			},
			wantText: "Searching.",
		},
		{
			name:      "first call at index zero is unchanged",
			blocks:    [][]string{anthropicToolUseBlock(0, "toolu_a", "read", `{"path":"a.go"}`), anthropicToolUseBlock(1, "toolu_b", "read", `{"path":"b.go"}`)},
			wantCalls: []ToolCall{{ID: "toolu_a", Name: "read", InputJSON: `{"path":"a.go"}`}, {ID: "toolu_b", Name: "read", InputJSON: `{"path":"b.go"}`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := append(anthropicEvents(tc.blocks...), anthropicMessageEnd("tool_use")...)
			p, done := anthropicStreamStub(t, anthropicSSE(t, events...))
			defer done()

			seen, onChunk := collectToolCalls()
			resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, onChunk)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if !reflect.DeepEqual(resp.ToolCalls, tc.wantCalls) {
				t.Errorf("response tool calls = %+v, want %+v", resp.ToolCalls, tc.wantCalls)
			}
			if resp.StopReason != "tool_use" || resp.Content != tc.wantText || resp.Reasoning != tc.wantThink {
				t.Errorf("response = stop %q content %q reasoning %q, want tool_use %q %q", resp.StopReason, resp.Content, resp.Reasoning, tc.wantText, tc.wantThink)
			}
			// A surface draws a pending row for every announced call, and only
			// the complete call closes it: whatever was announced is delivered.
			var wantIDs []string
			for _, c := range tc.wantCalls {
				wantIDs = append(wantIDs, c.ID)
				if seen.fragments[c.ID] != c.InputJSON {
					t.Errorf("argument fragments of %s = %q, want %q", c.ID, seen.fragments[c.ID], c.InputJSON)
				}
			}
			if !reflect.DeepEqual(seen.announced, wantIDs) {
				t.Errorf("announced = %v, want %v", seen.announced, wantIDs)
			}
			if !reflect.DeepEqual(seen.completed, tc.wantCalls) {
				t.Errorf("completed chunks = %+v, want %+v", seen.completed, tc.wantCalls)
			}
		})
	}
}

// A tool_use cut by max_tokens is returned like any other call: where it sits
// in the answer does not decide whether the reader keeps it. The same cut call
// comes back whether it is the first block or follows text.
func TestAnthropicStreamCutToolUseDoesNotDependOnItsIndex(t *testing.T) {
	cut := anthropicToolUseStart(0, "toolu_a", "write", `{"path":"a.go","content":"pack`)
	cutAfterText := append(append([]string{}, anthropicTextBlock(0, "Writing.")...), anthropicToolUseStart(1, "toolu_a", "write", `{"path":"a.go","content":"pack`)...)

	var got [2]*Response
	for i, events := range [][]string{anthropicEvents(cut), anthropicEvents(cutAfterText)} {
		events = append(events, anthropicMessageEnd("max_tokens")...)
		p, done := anthropicStreamStub(t, anthropicSSE(t, events...))
		resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
		done()
		if err != nil {
			t.Fatalf("Stream %d: %v", i, err)
		}
		got[i] = resp
	}
	if len(got[0].ToolCalls) != 1 {
		t.Fatalf("cut call as first block: tool calls = %+v, want the call", got[0].ToolCalls)
	}
	if !reflect.DeepEqual(got[1].ToolCalls, got[0].ToolCalls) {
		t.Errorf("cut call after text: tool calls = %+v, want %+v as for the first block", got[1].ToolCalls, got[0].ToolCalls)
	}
	if got[1].StopReason != "max_tokens" || got[1].Content != "Writing." {
		t.Errorf("response = stop %q content %q, want max_tokens and the text", got[1].StopReason, got[1].Content)
	}
}

// A user stop in the middle of a call keeps the calls started so far, the
// text before them next to it, wherever the calls sit in the answer.
func TestAnthropicStreamCancelKeepsToolUseBehindText(t *testing.T) {
	events := anthropicEvents(anthropicTextBlock(0, "Writing."), anthropicToolUseStart(1, "toolu_a", "write", `{"path":"a.go","content":"pack`))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicSSE(t, events...))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	p := newAnthropicProvider("test-model", "k", srv.URL, nil, 0, 0, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := p.Stream(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		if c.ToolCallDelta != nil {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a cancellation", err)
	}
	if resp == nil || resp.Content != "Writing." {
		t.Fatalf("resp = %+v, want the text kept", resp)
	}
	want := []ToolCall{{ID: "toolu_a", Name: "write", InputJSON: `{"path":"a.go","content":"pack`}}
	if !reflect.DeepEqual(resp.ToolCalls, want) {
		t.Errorf("tool calls = %+v, want %+v", resp.ToolCalls, want)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop reason = %q, want tool_use", resp.StopReason)
	}
}

// The non-streaming reader walks the content blocks in order and has no index
// to get wrong; this holds it to that.
func TestAnthropicCompleteToolUseBehindOtherBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"test-model","content":[`+
			`{"type":"thinking","thinking":"Hmm.","signature":"sig"},`+
			`{"type":"text","text":"Reading."},`+
			`{"type":"tool_use","id":"toolu_a","name":"read","input":{"path":"a.go"}},`+
			`{"type":"tool_use","id":"toolu_b","name":"glob","input":{"pattern":"*.go"}}],`+
			`"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	p := newAnthropicProvider("test-model", "k", srv.URL, nil, 0, 0, "")

	resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "toolu_a" || resp.ToolCalls[1].ID != "toolu_b" {
		t.Fatalf("tool calls = %+v, want toolu_a then toolu_b", resp.ToolCalls)
	}
	if resp.Content != "Reading." || resp.Reasoning != "Hmm." || resp.ReasoningSignature != "sig" || resp.StopReason != "tool_use" {
		t.Errorf("response = %+v", resp)
	}
}
