package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/tidwall/gjson"
)

func TestUnknownUsageDoesNotCalibrateContext(t *testing.T) {
	step := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok", StopReason: "end_turn", UsageIncomplete: true, InputTokens: 42}
	}
	ag, st := outputLimitAgent(t, scripted(step), nil)
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if b := st.GetLastContextBreakdown(); b != nil && b.ProviderInputTokens != 0 {
		t.Fatalf("unknown usage calibrated context: %+v", b)
	}
}

func TestTokenUsagePartialSumPersistsUnknown(t *testing.T) {
	first := func(_ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) *llm.Response {
		return &llm.Response{StopReason: "tool_use", InputTokens: 3, OutputTokens: 2, UsageKnown: true, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "missing_tool", InputJSON: `{}`}}}
	}
	second := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok", StopReason: "end_turn", UsageIncomplete: true}
	}
	ag, st := outputLimitAgent(t, scripted(first, second), nil)
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	value := gjson.GetBytes(data, "tokenUsageTotal.usageKnown")
	if !value.Exists() || value.Bool() {
		t.Fatalf("partial sum must be unknown, stats=%s", data)
	}
}

func TestLegacyUsageIsIncludedInNewTurn(t *testing.T) {
	step := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok", StopReason: "end_turn", UsageKnown: true, InputTokens: 3, OutputTokens: 2}
	}
	ag, st := outputLimitAgent(t, scripted(step), nil)
	if err := os.WriteFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"), []byte(`{"tokenUsageTotal":{"inputTokens":7,"outputTokens":4,"totalTokens":11}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(data, "tokenUsageTotal.totalTokens").Int(); got != 16 {
		t.Fatalf("total=%d want 16: %s", got, data)
	}
	if known := gjson.GetBytes(data, "tokenUsageTotal.usageKnown"); !known.Exists() || !known.Bool() {
		t.Fatalf("legacy usage became unknown: %s", data)
	}
}

func TestTokenUsageUnknownSurvivesNextTurn(t *testing.T) {
	first := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "a"})
		return &llm.Response{Content: "a", StopReason: "end_turn", UsageIncomplete: true}
	}
	second := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "b"})
		return &llm.Response{Content: "b", StopReason: "end_turn", UsageKnown: true, InputTokens: 3, OutputTokens: 2}
	}
	ag, st := outputLimitAgent(t, scripted(first, second), nil)
	for i := 0; i < 2; i++ {
		if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	value := gjson.GetBytes(data, "tokenUsageTotal.usageKnown")
	if !value.Exists() || value.Bool() {
		t.Fatalf("previous unknown usage lost: %s", data)
	}
}

func TestTokenUsagePresencePersists(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "zero"}[known], func(t *testing.T) {
			step := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
				cb(llm.StreamChunk{TextDelta: "ok"})
				return &llm.Response{Content: "ok", StopReason: "end_turn", UsageKnown: known, UsageIncomplete: !known}
			}
			ag, st := outputLimitAgent(t, scripted(step), nil)
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"))
			if err != nil {
				t.Fatal(err)
			}
			value := gjson.GetBytes(data, "tokenUsageTotal.usageKnown")
			if !value.Exists() || value.Bool() != known {
				t.Fatalf("usageKnown=%s want %v, stats=%s", value.Raw, known, data)
			}
		})
	}
}
