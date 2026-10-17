package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// rejectingSender answers every permission prompt with a no, the way a person
// who declines does.
type rejectingSender struct{ resumePermissionSender }

func (rejectingSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}, nil
}

// observeToolCalls records every tool call event until the test ends.
func observeToolCalls(t *testing.T) func() []ToolCallEvent {
	t.Helper()
	var mu sync.Mutex
	var events []ToolCallEvent
	remove := AddToolCallObserver(func(ev ToolCallEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	t.Cleanup(remove)
	return func() []ToolCallEvent {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(events)
	}
}

// Every call the loop decides on ends in exactly one event: the tool ran (ok
// or error, with how long it ran), it was refused before running, or the
// person said no. An MCP tool is reported as mcp and a name the agent does not
// know as other, so no name from outside the binary reaches an observer.
func TestToolCallObserverOneEventPerCall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := observeToolCalls(t)

	child, st, client := newChildAgentForTest(t, dir, []string{"read"})
	env := &tools.Env{CWD: dir, PermissionMode: config.PermModeBypass, SessionID: st.ID, Sender: client}
	run := func(ag *Agent, env *tools.Env, sessionID string, call llm.ToolCall) {
		t.Helper()
		_, _ = ag.executeToolCall(context.Background(), call, env, "agent", sessionID, false)
	}
	run(child, env, st.ID, llm.ToolCall{ID: "c1", Name: "read", InputJSON: `{"path":"a.txt"}`})
	run(child, env, st.ID, llm.ToolCall{ID: "c2", Name: "read", InputJSON: `{"path":"missing.txt"}`})
	run(child, env, st.ID, llm.ToolCall{ID: "c3", Name: "srv__tool", InputJSON: `{}`})
	run(child, env, st.ID, llm.ToolCall{ID: "c4", Name: "made_up_tool", InputJSON: `{}`})

	parent := &session.State{ID: "sess_tool_observer", CWD: dir, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	parent.SetPermissionMode(config.PermModeAsk)
	ag := NewAgent(&config.Config{}, parent, rejectingSender{}, nil)
	askEnv := &tools.Env{CWD: dir, PermissionMode: config.PermModeAsk, SessionID: parent.ID}
	run(ag, askEnv, parent.ID, llm.ToolCall{ID: "c5", Name: "write", InputJSON: `{"path":"b.txt","content":"x"}`})

	got := events()
	type call struct{ tool, outcome string }
	var calls []call
	for _, ev := range got {
		calls = append(calls, call{ev.Tool, ev.Outcome})
		ran := ev.Outcome == ToolOutcomeOK || ev.Outcome == ToolOutcomeError
		if !ran && ev.Duration != 0 {
			t.Errorf("%s %s carries a duration %v, but it never ran", ev.Tool, ev.Outcome, ev.Duration)
		}
	}
	want := []call{
		{"read", ToolOutcomeOK},
		{"read", ToolOutcomeError},
		{"mcp", ToolOutcomeRefused},
		{"other", ToolOutcomeRefused},
		{"write", ToolOutcomeDenied},
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("events = %v, want %v", calls, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("the declined write ran: %v", err)
	}
}

// A prompt nobody answered (the stream that carried it ended) is no outcome:
// the call is resumed later and counted when it runs, never twice.
func TestToolCallObserverSkipsAnUnansweredPrompt(t *testing.T) {
	dir := t.TempDir()
	events := observeToolCalls(t)
	st := &session.State{ID: "sess_tool_observer_unanswered", CWD: dir, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	st.SetPermissionMode(config.PermModeAsk)
	ag := NewAgent(&config.Config{}, st, unansweredSender{}, nil)
	env := &tools.Env{CWD: dir, PermissionMode: config.PermModeAsk, SessionID: st.ID}
	_, _ = ag.executeToolCall(context.Background(), llm.ToolCall{ID: "c1", Name: "write", InputJSON: `{"path":"b.txt","content":"x"}`}, env, "agent", st.ID, false)
	if got := events(); len(got) != 0 {
		t.Fatalf("an unanswered prompt produced events %+v", got)
	}
}

// unansweredSender fails every permission prompt, the way a surface whose
// stream ended before anybody answered does.
type unansweredSender struct{ resumePermissionSender }

func (unansweredSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return nil, context.Canceled
}

// The observer list is replaced whole, so removing an observer twice, or
// removing one while another stays, leaves the rest in place.
func TestToolCallObserverRemoval(t *testing.T) {
	var a, b int
	removeA := AddToolCallObserver(func(ToolCallEvent) { a++ })
	removeB := AddToolCallObserver(func(ToolCallEvent) { b++ })
	notifyToolCallObservers(ToolCallEvent{Tool: "read", Outcome: ToolOutcomeOK})
	removeA()
	removeA()
	notifyToolCallObservers(ToolCallEvent{Tool: "read", Outcome: ToolOutcomeOK})
	removeB()
	notifyToolCallObservers(ToolCallEvent{Tool: "read", Outcome: ToolOutcomeOK})
	if a != 1 || b != 2 {
		t.Fatalf("a = %d, b = %d; want 1 and 2", a, b)
	}
	if AddToolCallObserver(nil) == nil {
		t.Fatal("a nil observer must still return a remove function")
	}
}

// holdingSender keeps every prompt open until the test releases it, the way a
// person who has not answered yet does.
type holdingSender struct {
	resumePermissionSender
	entered chan string
	release chan struct{}
}

func (s holdingSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.entered <- "permission"
	<-s.release
	return &acp.PermissionResult{Outcome: "selected", OptionID: "reject"}, nil
}

func (s holdingSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	s.entered <- "question"
	<-s.release
	return &acp.QuestionResult{}, nil
}

// A permission prompt and a question each count as waiting for the person
// while they are open, and stop counting once answered.
func TestPendingPromptsCountWhatWaitsForAnAnswer(t *testing.T) {
	dir := t.TempDir()
	sender := holdingSender{entered: make(chan string, 1), release: make(chan struct{})}
	st := &session.State{ID: "sess_pending_prompts", CWD: dir, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	st.SetPermissionMode(config.PermModeAsk)
	ag := NewAgent(&config.Config{}, st, sender, nil)
	env := &tools.Env{CWD: dir, PermissionMode: config.PermModeAsk, SessionID: st.ID, Sender: sender}

	for _, tc := range []struct {
		kind string
		call llm.ToolCall
	}{
		{"permission", llm.ToolCall{ID: "p1", Name: "write", InputJSON: `{"path":"b.txt","content":"x"}`}},
		{"question", llm.ToolCall{ID: "q1", Name: "question", InputJSON: `{"questions":[{"header":"h","question":"Which?","options":[{"label":"a"}]}]}`}},
	} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = ag.executeToolCall(context.Background(), tc.call, env, "agent", st.ID, false)
		}()
		select {
		case got := <-sender.entered:
			if got != tc.kind {
				t.Fatalf("%s call asked a %s", tc.kind, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the %s call never asked", tc.kind)
		}
		permission, question := PendingPrompts()
		if want := map[string][2]int{"permission": {1, 0}, "question": {0, 1}}[tc.kind]; permission != want[0] || question != want[1] {
			t.Errorf("while a %s waits: permission %d, question %d; want %v", tc.kind, permission, question, want)
		}
		sender.release <- struct{}{}
		<-done
		if permission, question := PendingPrompts(); permission != 0 || question != 0 {
			t.Errorf("after the %s was answered: permission %d, question %d; want none", tc.kind, permission, question)
		}
	}
}
