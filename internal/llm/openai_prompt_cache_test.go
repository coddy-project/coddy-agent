package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func openAIRequestJSON(t *testing.T, model string, history []Message, tools []ToolDefinition) []byte {
	t.Helper()
	p := newOpenAIProvider(model, "", "", nil, 1024, 0, "")
	raw, err := json.Marshal(p.buildParams(history, tools, true))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A Claude model behind OpenRouter caches only what the request marks, as on
// the Messages API: the end of the system prompt and the last message the
// next request carries again. The turn context appended for one request stays
// unmarked.
func TestPromptCacheOpenRouterClaudeMarksTheSystemPromptAndTheTail(t *testing.T) {
	history := []Message{
		{Role: RoleSystem, Content: "You are Coddy."},
		{Role: RoleUser, Content: "read the manager"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", InputJSON: `{"path":"m.go"}`}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "package session"},
		{Role: RoleUser, Content: "<turn_context>now</turn_context>", Ephemeral: true},
	}
	tools := []ToolDefinition{{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object"}}}
	raw := openAIRequestJSON(t, "anthropic/claude-haiku-5.5", history, tools)

	if got := gjson.GetBytes(raw, "messages.0.content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("system prompt cache_control = %q, want ephemeral", got)
	}
	if got := gjson.GetBytes(raw, "messages.0.content.0.text").String(); got != "You are Coddy." {
		t.Fatalf("system prompt text = %q", got)
	}
	if got := gjson.GetBytes(raw, "messages.3.content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("tool result cache_control = %q, want ephemeral", got)
	}
	if got := gjson.GetBytes(raw, "messages.4.content"); got.Type != gjson.String {
		t.Fatalf("turn context = %s, want a plain unmarked string", got.Raw)
	}
	if n := strings.Count(string(raw), `"cache_control"`); n != 2 {
		t.Fatalf("%d cache_control marks, want 2", n)
	}
}

// An assistant message that only calls tools has no text to mark, so the
// tail mark moves back to the message before it; a user message with a
// picture is marked on its text.
func TestPromptCacheOpenRouterTailSkipsMessagesWithoutText(t *testing.T) {
	history := []Message{
		{Role: RoleUser, Content: "what is on it?", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,iVBORw0KGgo="}}},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", InputJSON: `{}`}}},
	}
	raw := openAIRequestJSON(t, "anthropic/claude-sonnet-5.5", history, nil)
	if got := gjson.GetBytes(raw, "messages.0.content.0.cache_control.type").String(); got != "ephemeral" {
		t.Fatalf("user text cache_control = %q, want ephemeral; request %s", got, raw)
	}
	if gjson.GetBytes(raw, "messages.0.content.1.cache_control").Exists() {
		t.Fatal("the picture is marked; the mark belongs on the text")
	}
	if n := strings.Count(string(raw), `"cache_control"`); n != 1 {
		t.Fatalf("%d cache_control marks, want 1", n)
	}
}

// Every other model caches a repeated prefix on its own: its request keeps
// plain string contents and carries no mark.
func TestPromptCacheOpenAIModelsCarryNoMarks(t *testing.T) {
	history := []Message{{Role: RoleSystem, Content: "You are Coddy."}, {Role: RoleUser, Content: "hi"}}
	for _, model := range []string{"gpt-5.5", "qwen3.6-35b-a3b", "google/gemini-3-pro", "deepseek/deepseek-v4"} {
		raw := openAIRequestJSON(t, model, history, nil)
		if strings.Contains(string(raw), "cache_control") {
			t.Fatalf("%s: request carries cache_control: %s", model, raw)
		}
		if gjson.GetBytes(raw, "messages.0.content").Type != gjson.String {
			t.Fatalf("%s: system content is no longer a string: %s", model, raw)
		}
	}
}
