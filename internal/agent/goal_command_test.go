package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestGoalCommandSetsShowsAndClearsSessionGoal(t *testing.T) {
	st := &session.State{ID: "sess_goal", CWD: t.TempDir(), Mode: session.ModeAgent}
	a := NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
	a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) {
		t.Fatal("/goal must not call the task model")
		return nil, nil
	})
	for _, command := range []string{"/goal ship the fix", "/goal", "/goal clear"} {
		stop, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: command}})
		if err != nil || stop != string(acp.StopReasonEndTurn) {
			t.Fatalf("%s: stop=%q err=%v", command, stop, err)
		}
	}
	if got := st.GetGoal(); got.Text != "" {
		t.Fatalf("goal remains after clear: %+v", got)
	}
	msgs := st.GetMessages()
	if len(msgs) != 6 || !strings.Contains(msgs[1].Content, "ship the fix") || !strings.Contains(msgs[3].Content, "ship the fix") {
		t.Fatalf("goal command transcript = %+v", msgs)
	}
}
