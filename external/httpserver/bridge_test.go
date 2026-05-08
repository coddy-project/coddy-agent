//go:build http

package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestForwardTextChunk_ReasoningEmittedAsReasoningContent(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: "silent plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"reasoning_content":"silent plan"`) {
		t.Fatalf("expected reasoning_content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, `"content":"silent plan"`) {
		t.Fatalf("reasoning must not map to delta.content, got: %s", raw)
	}
	var payload map[string]interface{}
	idx := strings.Index(raw, "{")
	if idx < 0 {
		t.Fatal("no json in response")
	}
	jsonLine := raw[idx:]
	if nl := strings.IndexByte(jsonLine, '\n'); nl >= 0 {
		jsonLine = jsonLine[:nl]
	}
	if err := json.Unmarshal([]byte(jsonLine), &payload); err != nil {
		t.Fatal(err)
	}
	choices, _ := payload["choices"].([]interface{})
	ch0 := choices[0].(map[string]interface{})
	delta := ch0["delta"].(map[string]interface{})
	if delta["reasoning_content"] != "silent plan" {
		t.Fatalf("delta: %#v", delta)
	}
	if _, has := delta["content"]; has {
		t.Fatalf("reasoning chunk should omit content field, delta=%#v", delta)
	}
}

func TestForwardTextChunk_TextUsesContentDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	sender := NewSender(&config.Config{}, rec, true, "agent-model")
	err := sender.SendSessionUpdate("sess-x", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"content":"hello"`) {
		t.Fatalf("expected content in SSE body, got: %s", raw)
	}
	if strings.Contains(raw, "reasoning_content") {
		t.Fatalf("text chunk must not set reasoning_content, got: %s", raw)
	}
}
