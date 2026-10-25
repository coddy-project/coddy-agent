package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// The Messages API caches nothing it is not asked to: a request marks the end
// of the system prompt, which caches the tool definitions with it, and the
// end of the conversation, so the next request reads every earlier turn back
// from the cache. Nothing else carries a marker.
func TestPromptCacheAnthropicMarksTheSystemPromptAndTheConversationTail(t *testing.T) {
	p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0, "")
	history := []Message{
		{Role: RoleSystem, Content: strings.Repeat("You are Coddy. ", 400)},
		{Role: RoleUser, Content: "read the manager"},
		{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "read", InputJSON: `{"path":"m.go"}`}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "package session"},
		{Role: RoleAssistant, Content: "It holds the sessions."},
		{Role: RoleUser, Content: "and the store?"},
	}
	tools := []ToolDefinition{
		{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{Name: "grep", Description: "Search", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}}}},
	}
	params, err := p.requestParams(history, tools)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(raw, "system.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("system prompt cache_control = %q, want ephemeral", got)
	}
	last := gjson.GetBytes(raw, "messages.#").Int() - 1
	tail := gjson.GetBytes(raw, "messages."+strconv.FormatInt(last, 10)+".content.@reverse.0.cache_control.type").String()
	if tail != "ephemeral" {
		t.Fatalf("last block of the last message cache_control = %q, want ephemeral", tail)
	}
	if n := strings.Count(string(raw), `"cache_control"`); n != 2 {
		t.Fatalf("%d cache_control markers, want 2 (the system prompt and the tail)", n)
	}
	if gjson.GetBytes(raw, "cache_control").Exists() {
		t.Fatal("a top-level cache_control rides on the request; Anthropic-compatible endpoints may not know it")
	}
}

// A request with no system prompt marks the tail only, and the history the
// caller passed is never written to: the marks live on this request.
func TestPromptCacheAnthropicMarksStayOnTheRequest(t *testing.T) {
	p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0, "")
	history := []Message{{Role: RoleUser, Content: "first"}, {Role: RoleAssistant, Content: "ok"}, {Role: RoleUser, Content: "second"}}
	params, err := p.requestParams(history, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(params)
	if n := strings.Count(string(raw), `"cache_control"`); n != 1 {
		t.Fatalf("%d cache_control markers without a system prompt, want 1", n)
	}
	again, _ := p.requestParams(history[:2], nil)
	if raw, _ := json.Marshal(again); strings.Contains(string(raw), `"text":"first","cache_control"`) {
		t.Fatal("a mark of an earlier request stayed on the shared history")
	}
}

// The turn context and the nudges the agent appends for one request are left
// unmarked: the next request does not carry them, so the mark goes on the
// last turn it does carry.
func TestPromptCacheAnthropicTailSkipsRequestOnlyMessages(t *testing.T) {
	p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0, "")
	history := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: "question"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "read", InputJSON: `{}`}}},
		{Role: RoleTool, ToolCallID: "c1", Content: "result"},
		{Role: RoleUser, Content: "<turn_context>now</turn_context>", Ephemeral: true},
	}
	params, err := p.requestParams(history, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(params)
	n := gjson.GetBytes(raw, "messages.#").Int()
	if gjson.GetBytes(raw, "messages."+strconv.FormatInt(n-1, 10)+".content.0.cache_control").Exists() {
		t.Fatal("the turn context carries the mark")
	}
	if got := gjson.GetBytes(raw, "messages."+strconv.FormatInt(n-2, 10)+".content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("the tool result before the turn context: cache_control %q, want ephemeral", got)
	}
}
