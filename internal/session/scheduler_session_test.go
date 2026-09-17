package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A scheduler job owns one session that every run of the job is a child of.
// It is a real bundle, hidden from the working list, and it never runs a turn
// of its own: every path that would start one refuses, naming the job.
func TestSchedulerJobSessionIsAReadOnlyTranscript(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	ctx := context.Background()
	id := session.NewSessionID()
	st, err := m.EnsureSchedulerJobSession(ctx, session.SchedulerJobSessionSpec{ID: id, JobID: "nightly", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if m.SessionByID(id) != st {
		t.Fatal("the job session must be the live session under its id")
	}
	if !st.IsSchedulerJob() || st.GetSchedulerJobID() != "nightly" {
		t.Fatalf("job marker lost: job=%v id=%q", st.IsSchedulerJob(), st.GetSchedulerJobID())
	}
	if !st.IsReadOnlyTranscript() {
		t.Fatal("a job session is a read-only transcript")
	}
	if st.IsSubagentRun() {
		t.Fatal("a job session is not a child of anything")
	}
	snap, err := store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Meta.SchedulerRun || snap.Meta.SchedulerJobID != "nightly" || snap.Meta.TitlePinned != "nightly" {
		t.Fatalf("persisted meta = %+v", snap.Meta)
	}

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}
	if _, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: id, Prompt: prompt}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("prompt error = %v, want ErrSchedulerSessionReadOnly", err)
	} else if !strings.Contains(err.Error(), "nightly") {
		t.Fatalf("the refusal must name the job: %v", err)
	}
	if _, err := m.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{SessionID: id, Prompt: prompt}, noopSender{}, &session.PromptRunOpts{DetachFromRequest: true}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("HTTP-style prompt error = %v", err)
	}
	if _, err := m.RunPlan(ctx, id, "any", noopSender{}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("RunPlan error = %v", err)
	}
	if _, _, err := m.BeginTurn(ctx, id, nil); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("BeginTurn error = %v", err)
	}
	if _, _, err := m.BeginSessionWork(ctx, id); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("BeginSessionWork error = %v", err)
	}
	if err := m.HandleSessionSetMode(ctx, acp.SessionSetModeParams{SessionID: id, ModeID: "plan"}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("SetMode error = %v", err)
	}
	if _, err := m.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{SessionID: id, ConfigID: "mode", Value: "plan"}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("SetConfigOption error = %v", err)
	}

	// Hidden from the working list, listed when the scheduler bundles are asked for.
	rows, err := store.ListSnapshotsWith(session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SessionID == id {
			t.Fatal("a job session must stay out of the default listing")
		}
	}
	rows, err = store.ListSnapshotsWith(session.ListOptions{IncludeSchedulerRuns: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.SessionID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("include_scheduler must list the job session")
	}

	// Ensure is idempotent on a live session, and restores the marker from disk
	// after the live entry was dropped.
	again, err := m.EnsureSchedulerJobSession(ctx, session.SchedulerJobSessionSpec{ID: id, JobID: "nightly", CWD: root})
	if err != nil || again != st {
		t.Fatalf("second Ensure must return the live state: %v %v", again == st, err)
	}
	m.ForgetLiveSession(id)
	restored, err := m.EnsureSchedulerJobSession(ctx, session.SchedulerJobSessionSpec{ID: id, JobID: "nightly", CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if restored == st || !restored.IsSchedulerJob() || restored.GetSchedulerJobID() != "nightly" {
		t.Fatalf("restored job session lost its marker: %+v", restored)
	}
	if _, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: id, Prompt: prompt}); !errors.Is(err, session.ErrSchedulerSessionReadOnly) {
		t.Fatalf("restored job session must stay read-only, got %v", err)
	}
	// A plain load through the ordinary path keeps the marker too.
	m.ForgetLiveSession(id)
	if _, err := m.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if loaded := m.SessionByID(id); loaded == nil || !loaded.IsSchedulerJob() {
		t.Fatal("HandleSessionLoad must restore the job marker")
	}
}

// A scheduled run has no parent to copy a tool set from, so the set is decided
// once the run's MCP clients are up, inside the manager, and stored before the
// first save.
func TestCreateSubagentSessionResolvesToolsAfterTheMCPDial(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	ctx := context.Background()
	jobID := session.NewSessionID()
	if _, err := m.EnsureSchedulerJobSession(ctx, session.SchedulerJobSessionSpec{ID: jobID, JobID: "nightly", CWD: root}); err != nil {
		t.Fatal(err)
	}
	childID := session.NewSessionID()
	var seen []string
	called := 0
	child, err := m.CreateSubagentSession(ctx, session.SubagentSpec{
		ID: childID, ParentSessionID: jobID, Name: "nightly", TaskID: "bg_1", CWD: root,
		ConnectMCP: true,
		ResolveTools: func(mcpTools []string) []string {
			called++
			seen = append([]string(nil), mcpTools...)
			return append([]string{"read", "run_command"}, mcpTools...)
		},
		Scheduler: &session.SchedulerRunMeta{JobID: "nightly", Trigger: "cron", FireSlot: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("resolver called %d times, want once", called)
	}
	if len(seen) != 0 {
		t.Fatalf("no MCP server is configured, the resolver must see no names, got %v", seen)
	}
	meta := child.Subagent()
	if meta == nil || strings.Join(meta.Tools, ",") != "read,run_command" {
		t.Fatalf("resolved tools not stored: %+v", meta)
	}
	if meta.Scheduler == nil || meta.Scheduler.JobID != "nightly" || meta.Scheduler.Trigger != "cron" || !meta.Scheduler.FireSlot.Equal(time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("scheduler meta lost: %+v", meta.Scheduler)
	}
	snap, err := store.ReadSnapshot(childID)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Meta.SubagentRun || snap.Meta.ParentSessionID != jobID || snap.Meta.SchedulerJobID != "nightly" || snap.Meta.SchedulerTrigger != "cron" || snap.Meta.SchedulerFireSlot != "2026-09-18T10:00:00Z" {
		t.Fatalf("persisted child meta = %+v", snap.Meta)
	}
	if snap.Meta.SchedulerRun {
		t.Fatal("a run bundle is a child, not a job session")
	}
	// The origin survives retirement and a load from disk, so the read-only
	// notice can still say which job the transcript belongs to.
	m.RetireSubagentSession(childID)
	if _, err := m.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: childID}); err != nil {
		t.Fatal(err)
	}
	restored := m.SessionByID(childID)
	if restored == nil || restored.Subagent() == nil || restored.Subagent().Scheduler == nil || restored.Subagent().Scheduler.JobID != "nightly" || restored.Subagent().Scheduler.Trigger != "cron" {
		t.Fatalf("restored run lost its scheduler origin: %+v", restored.Subagent())
	}
}

func TestResolveToolsReturningNothingFailsTheCreation(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	ctx := context.Background()
	jobID := session.NewSessionID()
	if _, err := m.EnsureSchedulerJobSession(ctx, session.SchedulerJobSessionSpec{ID: jobID, JobID: "nightly", CWD: root}); err != nil {
		t.Fatal(err)
	}
	childID := session.NewSessionID()
	_, err := m.CreateSubagentSession(ctx, session.SubagentSpec{
		ID: childID, ParentSessionID: jobID, Name: "nightly", TaskID: "bg_1", CWD: root,
		ResolveTools: func([]string) []string { return nil },
	})
	if err == nil {
		t.Fatal("a resolver that leaves no tool must fail the creation")
	}
	if m.SessionByID(childID) != nil {
		t.Fatal("the failed child must not stay live")
	}
	if store.HasPersistedSnapshot(childID) {
		t.Fatal("the failed child must leave no bundle")
	}
}
