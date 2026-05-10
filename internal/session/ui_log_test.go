package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestCountUserTurns(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
	}
	if n := CountUserTurns(msgs); n != 2 {
		t.Fatalf("got %d", n)
	}
	if n := CountUserTurns(nil); n != 0 {
		t.Fatalf("nil: got %d", n)
	}
}
