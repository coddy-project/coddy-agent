package session_test

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func TestStatisticsSourceReachesOnlyItsTurn(t *testing.T) {
	seen := make(chan string, 2)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(acpToLLM(prompt))
		seen <- statistics.Source(ctx)
		return string(acp.StopReasonEndTurn), nil
	}
	m, _, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	prompt := acp.SessionPromptParams{SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}}
	for _, source := range []string{"webui", ""} {
		if _, err := m.HandleSessionPromptWithSender(context.Background(), prompt, noopSender{}, &session.PromptRunOpts{StatisticsSource: source}); err != nil {
			t.Fatal(err)
		}
		if got := <-seen; got != source {
			t.Fatalf("turn source = %q, want %q", got, source)
		}
	}
}
