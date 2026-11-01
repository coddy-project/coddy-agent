package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestStatsReadErrorDoesNotOverwriteExistingFile(t *testing.T) {
	step := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
		cb(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok", StopReason: "end_turn", UsageKnown: true, InputTokens: 3, OutputTokens: 2}
	}
	ag, st := outputLimitAgent(t, scripted(step), nil)
	path := filepath.Join(st.GetPersistedSessionDir(), "stats.json")
	original := `{"tokenUsageTotal":{"inputTokens":7,"outputTokens":4},`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("stats overwritten after read error: got %s, want %s", data, original)
	}
}
