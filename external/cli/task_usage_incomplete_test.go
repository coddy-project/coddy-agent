//go:build cli

package cli

import (
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"strings"
	"testing"
)

func TestIncompleteAgentTaskUsageHidesPartialTotal(t *testing.T) {
	row := bgtask.Snapshot{Agent: &bgtask.AgentInfo{Model: "demo/model", InputTokens: 1200, OutputTokens: 100, UsageIncomplete: true}}
	got := strings.Join(agentUsageParts(row), " ")
	if got != "model" {
		t.Fatalf("got %q, want model alone", got)
	}
}
