package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestTokenUsageEventSeparatesSessionFromCall(t *testing.T) {
	for _, stats := range []string{`{"tokenUsageTotal":{"usageKnown":false}}`, `{"tokenUsageTotal":`} {
		t.Run(stats, func(t *testing.T) {
			sender := &outputLimitSender{}
			step := func(_ []llm.Message, _ []llm.ToolDefinition, cb func(llm.StreamChunk)) *llm.Response {
				cb(llm.StreamChunk{TextDelta: "ok"})
				return &llm.Response{Content: "ok", StopReason: "end_turn", UsageKnown: true, InputTokens: 3, OutputTokens: 2}
			}
			ag, st := outputLimitAgentWith(t, scripted(step), nil, sender)
			if err := os.WriteFile(filepath.Join(st.GetPersistedSessionDir(), "stats.json"), []byte(stats), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
				t.Fatal(err)
			}
			for _, update := range sender.updates {
				if u, ok := update.(acp.TokenUsageUpdate); ok {
					b, err := json.Marshal(u)
					if err != nil {
						t.Fatal(err)
					}
					var got map[string]interface{}
					if err := json.Unmarshal(b, &got); err != nil {
						t.Fatal(err)
					}
					if got["usageKnown"] != true || got["sessionUsageKnown"] != false {
						t.Fatalf("usage flags: %s", b)
					}
					return
				}
			}
			t.Fatal("no token usage update")
		})
	}
}
