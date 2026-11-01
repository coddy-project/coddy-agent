//go:build http

package httpserver

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"testing"
)

func TestCompletionUsageIncompleteAggregate(t *testing.T) {
	yes, no := true, false
	known := acp.TokenUsageUpdate{UsageKnown: &yes, InputTokens: 3, OutputTokens: 2}
	unknown := acp.TokenUsageUpdate{UsageKnown: &no}
	for _, updates := range [][]acp.TokenUsageUpdate{{known, unknown}, {unknown, known}} {
		var u completionUsage
		for _, update := range updates {
			u.add(update)
		}
		if got := u.openAI(); got != nil {
			t.Fatalf("partial sum presented as total: %v", got)
		}
	}
}
