package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// recordingPromptRunner stands in for the session manager and records the one
// prompt a woken turn submits.
type recordingPromptRunner struct {
	calls  int
	params acp.SessionPromptParams
	sender acp.UpdateSender
	opts   *session.PromptRunOpts
	err    error
}

func (r *recordingPromptRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.calls++
	r.params, r.sender, r.opts = params, sender, opts
	return nil, r.err
}

// updateLog is the stand-in for the ACP server the client listens on.
type updateLog struct {
	mu      sync.Mutex
	updates []interface{}
}

func (l *updateLog) SendSessionUpdate(_ string, u interface{}) error {
	l.mu.Lock()
	l.updates = append(l.updates, u)
	l.mu.Unlock()
	return nil
}

func (l *updateLog) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

func (l *updateLog) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func finishedWake(sessionID string) agent.Wake {
	end := time.Now()
	code := 2
	return agent.Wake{SessionID: sessionID, Tasks: []bgtask.Snapshot{{
		ID: "bg_3", SessionID: sessionID, Kind: bgtask.KindCommand, Label: "make test",
		Status: bgtask.StatusFailed, ExitCode: &code, StartedAt: end.Add(-90 * time.Second), FinishedAt: &end,
	}}}
}

func TestACPWakeRunnerPromptsTheSessionThroughTheClientSender(t *testing.T) {
	mgr := &recordingPromptRunner{}
	sender := &updateLog{}

	if err := acpWakeRunner(mgr, sender)(context.Background(), finishedWake("sess_1")); err != nil {
		t.Fatalf("wake turn: %v", err)
	}
	if mgr.calls != 1 || mgr.params.SessionID != "sess_1" {
		t.Fatalf("calls = %d, params = %+v", mgr.calls, mgr.params)
	}
	if len(mgr.params.Prompt) != 1 || !strings.Contains(mgr.params.Prompt[0].Text, "bg_3") {
		t.Fatalf("prompt = %+v, want the wake instruction", mgr.params.Prompt)
	}
	// The updates of a turn nobody asked for must still reach the editor, and
	// a gated tool call is asked there like any other.
	if mgr.sender != acp.UpdateSender(sender) {
		t.Fatalf("sender = %T, want the ACP server sender", mgr.sender)
	}
	if mgr.opts == nil || !mgr.opts.SkipUsagePublish || mgr.opts.BackgroundWake == nil || mgr.opts.BackgroundWake.Tasks[0].ID != "bg_3" {
		t.Fatalf("opts = %+v, want the wake marker and no usage refresh", mgr.opts)
	}
}

func TestACPWakeRunnerReportsABusySessionSoTheWakerCanWait(t *testing.T) {
	mgr := &recordingPromptRunner{err: session.ErrSessionTurnBusy}
	err := acpWakeRunner(mgr, &updateLog{})(context.Background(), finishedWake("sess_1"))
	if !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("error = %v, want ErrSessionTurnBusy", err)
	}
}

// An editor that renders none of Coddy's own updates still reads what woke the
// agent: the wake goes out as it is, followed by a quoted note at the head of
// the answer. Every other update passes through untouched.
func TestACPWakeNoticeAddsATextNoteForEditors(t *testing.T) {
	inner := &updateLog{}
	out := acpWakeNotice{inner}
	two := 2
	wake := acp.BackgroundWakeUpdate{SessionUpdate: acp.UpdateTypeBackgroundWake, Tasks: []acp.BackgroundWakeTask{
		{ID: "bg_3", Kind: "command", Label: "make test", Status: "failed", ExitCode: &two, DurationMs: 90_000},
	}}
	chunk := acp.MessageChunkUpdate{SessionUpdate: acp.UpdateTypeAgentMessageChunk, Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "The tests failed."}}
	if err := out.SendSessionUpdate("sess_1", wake); err != nil {
		t.Fatal(err)
	}
	if err := out.SendSessionUpdate("sess_1", chunk); err != nil {
		t.Fatal(err)
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if len(inner.updates) != 3 {
		t.Fatalf("updates = %+v, want the wake, the note and the answer", inner.updates)
	}
	if _, ok := inner.updates[0].(acp.BackgroundWakeUpdate); !ok {
		t.Fatalf("first update = %T, want the wake itself", inner.updates[0])
	}
	note, ok := inner.updates[1].(acp.MessageChunkUpdate)
	if !ok || note.SessionUpdate != acp.UpdateTypeAgentMessageChunk {
		t.Fatalf("second update = %+v, want an agent text chunk", inner.updates[1])
	}
	want := "> Woken by a finished background task: bg_3 make test, failed, exit 2, 1m 30s\n\n"
	if note.Content.Text != want {
		t.Fatalf("note = %q, want %q", note.Content.Text, want)
	}
	if inner.updates[2] != interface{}(chunk) {
		t.Fatalf("third update = %+v, want the answer untouched", inner.updates[2])
	}
}
