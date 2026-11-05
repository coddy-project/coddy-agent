package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// acpToolRowSeen reports whether the surface has been told of the call id.
func acpToolRowSeen(s *acpSender, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.updates {
		if tc, ok := u.(acp.ToolCallUpdate); ok && tc.ToolCallID == id {
			return true
		}
	}
	return false
}

// A steer follow-up queued while an agent behind an acp row works is read at
// once, as it is with a model of its own. Such a turn is one call that can
// last minutes with no step of Coddy's inside it, so the message used to wait
// for the whole run and the only way to be heard was Stop. Its step boundary
// is the prompt: the run is interrupted and started again carrying the queue.
//
// The scripted agent holds its first answer until session/cancel arrives, so
// nothing but the follow-up can end this turn: without the interrupt the
// prompt never returns.
func TestACPSteerFollowUpInterruptsTheAgentsPrompt(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{
		{
			Tools:      []acptest.Tool{{ID: "t1", Title: "Run go test", Kind: "execute"}},
			Text:       []string{"working on the first thing"},
			WaitCancel: true,
		},
		{Echo: true},
	}})
	sender := &acpSender{}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return NewAgent(cfg, st, snd, slog.Default()).Run(ctx, prompt)
	}
	m := session.NewManager(cfg, sender, runner, slog.Default(), t.TempDir(), nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{
			SessionID: res.SessionID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the first thing"}},
		})
		done <- err
	}()

	// The agent is inside its prompt once its first call reached the surface.
	deadline := time.Now().Add(30 * time.Second)
	for !acpToolRowSeen(sender, "acp_t1") {
		if time.Now().After(deadline) {
			t.Fatal("the agent never started its first call")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, _, err := m.EnqueueTurnMessageWithMode(res.SessionID, "also do the second thing", session.QueueModeSteer, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the queued follow-up did not reach the agent: its prompt was never interrupted")
	}

	prompts := acpPrompts(t, logPath)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %d, want the follow-up sent as a prompt of its own: %q", len(prompts), prompts)
	}
	if !strings.HasPrefix(prompts[1], "also do the second thing") {
		t.Fatalf("second prompt = %q", prompts[1])
	}
	st := m.SessionByID(res.SessionID)
	if left := st.QueuedMessages(); len(left) != 0 {
		t.Fatalf("queue left = %+v", left)
	}
	var users []string
	for _, msg := range st.GetMessages() {
		if msg.Role == "user" {
			users = append(users, msg.Content)
		}
	}
	if len(users) != 2 || users[1] != "also do the second thing" {
		t.Fatalf("user rows = %q", users)
	}
}

// A follow-up that asked to wait for the turn is not a steer: it never
// interrupts the agent, and the turn reads it at its own boundary.
func TestACPAfterTurnFollowUpDoesNotInterruptTheAgent(t *testing.T) {
	cfg, logPath := acpLoopConfig(t, acptest.Script{Turns: []acptest.Turn{
		{
			Tools: []acptest.Tool{{ID: "t1", Title: "Run go test", Kind: "execute", DelayMS: 300}},
			Text:  []string{"working on the first thing"},
		},
		{Echo: true},
	}})
	sender := &acpSender{}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return NewAgent(cfg, st, snd, slog.Default()).Run(ctx, prompt)
	}
	m := session.NewManager(cfg, sender, runner, slog.Default(), t.TempDir(), nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{
			SessionID: res.SessionID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the first thing"}},
		})
		done <- err
	}()
	deadline := time.Now().Add(30 * time.Second)
	for !acpToolRowSeen(sender, "acp_t1") {
		if time.Now().After(deadline) {
			t.Fatal("the agent never started its first call")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, _, err := m.EnqueueTurnMessageWithMode(res.SessionID, "and then the second thing", session.QueueModeAfterTurn, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the turn did not finish")
	}
	// The first prompt ran to its end: its answer is in the transcript, not a
	// cancellation, and the follow-up went as a second prompt after it.
	prompts := acpPrompts(t, logPath)
	if len(prompts) != 2 || !strings.HasPrefix(prompts[1], "and then the second thing") {
		t.Fatalf("prompts = %q", prompts)
	}
	st := m.SessionByID(res.SessionID)
	var answers []string
	for _, msg := range st.GetMessages() {
		if msg.Role == "assistant" {
			answers = append(answers, msg.Content)
		}
	}
	if len(answers) == 0 || !strings.Contains(answers[0], "working on the first thing") {
		t.Fatalf("the first answer was cut short: %q", answers)
	}
}
