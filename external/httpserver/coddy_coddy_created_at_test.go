//go:build http

package httpserver

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestLlmMsgsToCoddyOpenAIIncludesCreatedAt(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "u", CreatedAt: "2026-05-01T12:00:00Z"},
		{Role: llm.RoleAssistant, Content: "a", CreatedAt: "2026-05-01T12:00:01Z"},
	})
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if got, _ := out[0]["created_at"].(string); got != "2026-05-01T12:00:00Z" {
		t.Fatalf("user created_at: %#v", out[0])
	}
	if got, _ := out[1]["created_at"].(string); got != "2026-05-01T12:00:01Z" {
		t.Fatalf("assistant created_at: %#v", out[1])
	}
}

func TestLlmMsgsToCoddyOpenAIOmitsEmptyCreatedAt(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "u"},
	})
	if _, ok := out[0]["created_at"]; ok {
		t.Fatalf("expected no created_at, got %#v", out[0])
	}
}
