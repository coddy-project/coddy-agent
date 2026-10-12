package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// questionWaitSender holds every question until the test answers it.
type questionWaitSender struct {
	todoSnapshotSender
	asked  chan acp.QuestionRequestParams
	answer chan *acp.QuestionResult
}

func (s *questionWaitSender) RequestQuestion(ctx context.Context, p acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	s.asked <- p
	select {
	case res := <-s.answer:
		return res, nil
	case <-ctx.Done():
		return &acp.QuestionResult{}, ctx.Err()
	}
}

// A question the model asks blocks the turn until a person answers it, under
// bypass too (#443): bypass decides what runs without approval, not what the
// agent may decide for the person it asked.
func TestQuestionBlocksTheTurnUnderBypass(t *testing.T) {
	dir := t.TempDir()
	st := &session.State{ID: "sess_question_bypass", CWD: dir, Mode: session.ModeAgent, SessionDir: dir}
	cfg := &config.Config{
		Paths: config.Paths{Home: t.TempDir()},
		Tools: config.Tools{PermissionMode: config.PermModeBypass},
	}
	sender := &questionWaitSender{asked: make(chan acp.QuestionRequestParams, 1), answer: make(chan *acp.QuestionResult, 1)}
	ag := NewAgent(cfg, st, sender, nil)
	args := `{"questions":[{"question":"Which database?","options":[{"label":"postgres"},{"label":"sqlite"}]}]}`

	type outcome struct {
		res string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := ag.executeToolCall(context.Background(),
			llm.ToolCall{ID: "call_question_1", Name: "question", InputJSON: args},
			ag.buildToolEnv(string(session.ModeAgent), dir), string(session.ModeAgent), st.ID, false)
		done <- outcome{res, err}
	}()

	select {
	case p := <-sender.asked:
		if len(p.Questions) != 1 || p.Questions[0].Question != "Which database?" {
			t.Fatalf("the surface was asked %+v", p.Questions)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question never reached the surface")
	}
	select {
	case o := <-done:
		t.Fatalf("the call returned before anybody answered: %q %v", o.res, o.err)
	case <-time.After(200 * time.Millisecond):
	}
	sender.answer <- &acp.QuestionResult{Answers: [][]string{{"sqlite"}}}
	select {
	case o := <-done:
		if o.err != nil || !strings.Contains(o.res, "sqlite") {
			t.Fatalf("result = %q %v, want the answer", o.res, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not return after the answer")
	}
}
