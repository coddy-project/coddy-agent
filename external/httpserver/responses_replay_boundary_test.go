//go:build http

package httpserver

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestResponsesReplaysItsOwnMixedOutput(t *testing.T) {
	response := &llm.Response{Content: "I will read the file.", Reasoning: "check the fixture", ToolCalls: []llm.ToolCall{{ID: "call_read", Name: "read", InputJSON: `{"path":"note.txt"}`}}}
	items := responsesOutputItems(response)
	items = append(items, map[string]any{"type": "function_call_output", "call_id": "call_read", "output": "fixture"}, map[string]any{"role": "user", "content": "continue"})
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := responsesInputToMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("one assistant turn split into %d messages: %+v", len(messages), messages)
	}
	assistant := messages[0]
	if assistant.Role != llm.RoleAssistant || assistant.Content != response.Content || assistant.Reasoning != response.Reasoning || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0] != response.ToolCalls[0] {
		t.Fatalf("assistant changed: %+v", assistant)
	}
	if messages[1].Role != llm.RoleTool || messages[1].ToolCallID != "call_read" || messages[1].Content != "fixture" {
		t.Fatalf("tool result is not paired: %+v", messages)
	}
}

func TestResponsesPreservesReplayTextExactly(t *testing.T) {
	messages, err := responsesInputToMessages(json.RawMessage(`[{"role":"assistant","content":"  before\n"},{"role":"assistant","content":"after  "},{"role":"user","content":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != "  before\nafter  " || messages[1].Content != "ab" {
		t.Fatalf("replay text changed: %+v", messages)
	}
}

func TestResponsesRejectsUnsupportedNestedData(t *testing.T) {
	cases := []struct{ name, input string }{
		{"encrypted reasoning", `[{"type":"reasoning","encrypted_content":"opaque","summary":[]},{"role":"user","content":"hi"}]`},
		{"unknown summary", `[{"type":"reasoning","summary":[{"type":"future_summary","text":"must not drop"}]},{"role":"user","content":"hi"}]`},
		{"unknown item field", `[{"role":"user","content":"hi","future_option":true}]`},
		{"unknown part field", `[{"role":"user","content":[{"type":"input_text","text":"hi","future_option":true}]}]`},
		{"missing call id", `[{"type":"function_call","id":"fc_item","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"fc_item","output":"ok"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if messages, err := responsesInputToMessages(json.RawMessage(tc.input)); err == nil {
				t.Fatalf("unsupported data silently accepted: %+v", messages)
			}
		})
	}
}

func TestResponsesRejectsOrphanReasoning(t *testing.T) {
	for _, input := range []string{
		`[{"type":"reasoning","summary":[{"type":"summary_text","text":"orphan"}]},{"role":"user","content":"hi"}]`,
		`[{"type":"reasoning","summary":[{"type":"summary_text","text":"orphan"}]},{"role":"user","content":"hi"},{"role":"assistant","content":"later"},{"role":"user","content":"continue"}]`,
	} {
		if messages, err := responsesInputToMessages(json.RawMessage(input)); err == nil {
			t.Fatalf("orphan reasoning accepted or moved across turns: %+v", messages)
		}
	}
}

func TestResponsesDoesNotPretendToEnforceStrictTools(t *testing.T) {
	for _, extra := range []string{`"strict":true`, `"future_option":true`} {
		raw := json.RawMessage(`[{"type":"function","name":"read","parameters":{"type":"object"},` + extra + `}]`)
		if _, err := parseResponsesTools(raw); err == nil {
			t.Errorf("unsupported tool option silently ignored: %s", raw)
		}
	}
}

func TestResponsesFunctionOutputInputTextParts(t *testing.T) {
	messages, err := responsesInputToMessages(json.RawMessage(`[{"type":"function_call","call_id":"call_read","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"call_read","output":[{"type":"input_text","text":"fixture"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "fixture" {
		t.Fatalf("tool output changed: %+v", messages)
	}
}

func TestResponsesRefusalTextIsNotSilentlyDropped(t *testing.T) {
	messages, err := responsesInputToMessages(json.RawMessage(`[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"Not allowed."}]},{"role":"user","content":"why?"}]`))
	if err == nil && (len(messages) != 2 || messages[0].Content != "Not allowed.") {
		t.Fatalf("refusal silently lost: %+v", messages)
	}
}
