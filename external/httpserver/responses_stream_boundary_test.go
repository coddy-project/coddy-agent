//go:build http

package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/tidwall/gjson"
)

func TestResponsesStreamInterleavedTextIsNotDuplicated(t *testing.T) {
	call := llm.ToolCall{ID: "call_1", Name: "read", InputJSON: `{}`}
	p := &responsesStubProvider{chunks: []llm.StreamChunk{{TextDelta: "before"}, {ToolCall: &call}, {TextDelta: "after"}}, reply: &llm.Response{Content: "beforeafter", ToolCalls: []llm.ToolCall{call}, StopReason: "tool_use"}}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
	raw, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	frames := parseSSEFrames(string(raw))
	if len(frames) == 0 {
		t.Fatalf("no frames: %s", raw)
	}
	var deltas strings.Builder
	for _, frame := range frames {
		if frame.event == "response.output_text.delta" {
			deltas.WriteString(gjson.Get(frame.data, "delta").String())
		}
	}
	var text strings.Builder
	for _, item := range gjson.Get(frames[len(frames)-1].data, "response.output").Array() {
		if item.Get("type").String() == "message" {
			text.WriteString(item.Get("content.0.text").String())
		}
	}
	if deltas.String() != "beforeafter" || text.String() != "beforeafter" {
		t.Fatalf("deltas=%q output=%q: %s", deltas.String(), text.String(), raw)
	}
}

func TestResponsesIncompleteOutputStatus(t *testing.T) {
	p := &responsesStubProvider{reply: &llm.Response{Content: "partial", StopReason: "max_tokens"}}
	_, ts := stubResponsesServer(t, p)
	res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":false}`, nil)
	raw, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(raw, "status").String() != "incomplete" || gjson.GetBytes(raw, "output.0.status").String() != "incomplete" {
		t.Fatalf("inconsistent incomplete response: %s", raw)
	}
}

func TestResponsesStreamTerminalStatus(t *testing.T) {
	for _, tc := range []struct{ name, streamed, final, stop, event string }{
		{"incomplete", "partial", "partial", "max_tokens", "response.incomplete"},
		{"contradictory final", "partial", "different", "end_turn", "response.failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &responsesStubProvider{chunks: []llm.StreamChunk{{TextDelta: tc.streamed}}, reply: &llm.Response{Content: tc.final, StopReason: tc.stop}}
			_, ts := stubResponsesServer(t, p)
			res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
			raw, err := ioReadAllClose(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			frames := parseSSEFrames(string(raw))
			if len(frames) == 0 || frames[len(frames)-1].event != tc.event {
				t.Fatalf("missing terminal %s: %s", tc.event, raw)
			}
		})
	}
}

func TestResponsesStreamCompletedToolRejectsChangedFinalArguments(t *testing.T) {
	w := httptest.NewRecorder()
	s := newResponsesStream(w, func() {})
	call := llm.ToolCall{ID: "c1", Name: "read", InputJSON: "{}"}
	s.chunk(llm.StreamChunk{ToolCall: &call})
	changed := call
	changed.InputJSON = `{"path":"other"}`
	output, err := s.finish(&llm.Response{ToolCalls: []llm.ToolCall{changed}}, nil)
	if err == nil {
		t.Fatal("changed final arguments accepted")
	}
	item := output[0].(map[string]any)
	if item["arguments"] != "{}" || item["status"] != "completed" {
		t.Fatalf("completed item changed: %+v", item)
	}
	frames := parseSSEFrames(w.Body.String())
	done := false
	for _, frame := range frames {
		if frame.event == "response.output_item.done" {
			done = true
		}
		if done && frame.event == "response.function_call_arguments.delta" {
			t.Fatal("delta after item done")
		}
	}
}

func TestResponsesStreamContradictionStopsSubsequentItems(t *testing.T) {
	t.Run("final text before tool", func(t *testing.T) {
		p := &responsesStubProvider{chunks: []llm.StreamChunk{{TextDelta: "original"}}, reply: &llm.Response{Content: "different", ToolCalls: []llm.ToolCall{{ID: "new", Name: "read", InputJSON: "{}"}}}}
		_, ts := stubResponsesServer(t, p)
		res := postResponses(t, ts, `{"model":"openai/gpt-4o","input":"hi","stream":true}`, nil)
		raw, err := ioReadAllClose(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		frames := parseSSEFrames(string(raw))
		if len(frames) == 0 || frames[len(frames)-1].event != "response.failed" {
			t.Fatalf("missing failed terminal: %s", raw)
		}
		for _, f := range frames {
			if f.event == "response.output_item.added" && gjson.Get(f.data, "item.type").String() == "function_call" {
				t.Fatalf("tool added after contradiction: %s", raw)
			}
		}
	})
	t.Run("multi-stage chunk", func(t *testing.T) {
		w := httptest.NewRecorder()
		s := newResponsesStream(w, func() {})
		done := llm.ToolCall{ID: "done", Name: "read", InputJSON: "{}"}
		s.chunk(llm.StreamChunk{ToolCall: &done})
		before := len(parseSSEFrames(w.Body.String()))
		late := llm.ToolCall{ID: "done", Name: "read", InputJSON: "x"}
		extra := llm.ToolCall{ID: "extra", Name: "read", InputJSON: "{}"}
		s.chunk(llm.StreamChunk{ToolCallDelta: &late, ToolCall: &extra})
		_, err := s.finish(nil, nil)
		if err == nil {
			t.Fatal("missing protocol error")
		}
		for _, f := range parseSSEFrames(w.Body.String())[before:] {
			if f.event == "response.output_item.added" || f.event == "response.function_call_arguments.delta" {
				t.Fatalf("new item or delta after contradiction: %s", w.Body.String())
			}
		}
	})
}
