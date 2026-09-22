package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// newRewindState registers a live State with a persisted bundle for id.
func newRewindState(t *testing.T, mgr *Manager, fs *FileStore, id string, msgs []llm.Message) *State {
	t.Helper()
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	st := &State{SessionDir: dir}
	for _, m := range msgs {
		st.AddMessage(m)
	}
	if mgr.sessions == nil {
		mgr.sessions = map[string]*State{}
	}
	mgr.sessions[id] = st
	return st
}

func TestRewindTruncatesInPlace(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))
	before := st.MessagesRev()

	rev, err := mgr.RewindSession("s1", 1)
	if err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	if rev <= before {
		t.Fatalf("messagesRev did not advance: before=%d after=%d", before, rev)
	}
	msgs := st.GetMessages()
	if len(msgs) != 2 || msgs[0].Role != llm.RoleUser || msgs[0].Content != "hello" || msgs[1].Role != llm.RoleAssistant {
		t.Fatalf("unexpected surviving history: %+v", msgs)
	}
}

func TestRewindAtFirstUserMessage(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))

	if _, err := mgr.RewindSession("s1", 0); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	if msgs := st.GetMessages(); len(msgs) != 0 {
		t.Fatalf("expected empty history, got %d messages", len(msgs))
	}
}

func TestRewindOutOfRange(t *testing.T) {
	mgr, fs := newTestManager(t)
	newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))

	for _, idx := range []int{2, 5, -1} {
		if _, err := mgr.RewindSession("s1", idx); !errors.Is(err, ErrRewindOutOfRange) {
			t.Fatalf("index %d: want ErrRewindOutOfRange, got %v", idx, err)
		}
	}
}

func TestRewindNotLoaded(t *testing.T) {
	mgr, fs := newTestManager(t)
	if _, err := fs.EnsureLayout("s1"); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if _, err := mgr.RewindSession("s1", 0); err == nil {
		t.Fatalf("expected an error for a session that is not loaded")
	}
}

func TestRewindCountsBackgroundWake(t *testing.T) {
	mgr, fs := newTestManager(t)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "ask"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "task done", BackgroundWake: &llm.BackgroundWake{Tasks: []llm.BackgroundWakeTask{{ID: "bg_1"}}}},
		{Role: llm.RoleAssistant, Content: "continue"},
		{Role: llm.RoleUser, Content: "next"},
		{Role: llm.RoleAssistant, Content: "ok2"},
	}
	st := newRewindState(t, mgr, fs, "s1", msgs)

	// The wake is the second user-role row: index 1 rewinds right before it.
	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	if got := st.GetMessages(); len(got) != 2 {
		t.Fatalf("expected the first turn only, got %+v", got)
	}
}

func TestRewindRefusesSubagent(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("task"))
	st.SetSubagentMeta(SubagentMeta{Name: "explore", ParentSessionID: "p1", TaskID: "bg_1"})

	if _, err := mgr.RewindSession("s1", 0); !errors.Is(err, ErrSubagentReadOnly) {
		t.Fatalf("want ErrSubagentReadOnly, got %v", err)
	}
}

func TestRewindRefusesScheduler(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("job"))
	st.SetSchedulerJobWithoutPersist("job_1")

	if _, err := mgr.RewindSession("s1", 0); !errors.Is(err, ErrSchedulerSessionReadOnly) {
		t.Fatalf("want ErrSchedulerSessionReadOnly, got %v", err)
	}
}

func TestRewindRefusesActiveTurn(t *testing.T) {
	mgr, fs := newTestManager(t)
	newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))

	release := mgr.markTurnActive("s1")
	defer release()

	if _, err := mgr.RewindSession("s1", 1); !errors.Is(err, ErrSessionTurnActive) {
		t.Fatalf("want ErrSessionTurnActive, got %v", err)
	}
}

func TestRewindPrunesUILog(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world", "again"))
	st.AppendUILogError(1, "first turn error")
	st.AppendUILogError(2, "second turn error")
	st.AppendUILogNotice(3, "third turn notice")

	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	log := st.GetUILog()
	if len(log) != 1 || log[0].UserTurnIndex != 1 {
		t.Fatalf("expected only the first turn's entry, got %+v", log)
	}

	if _, err := mgr.RewindSession("s1", 0); err != nil {
		t.Fatalf("RewindSession to start: %v", err)
	}
	if log := st.GetUILog(); len(log) != 0 {
		t.Fatalf("expected an empty ui log, got %+v", log)
	}
}

func TestRewindCleansArtifacts(t *testing.T) {
	mgr, fs := newTestManager(t)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "ask"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_keep", Name: "read"}}},
		{Role: llm.RoleTool, ToolCallID: "call_keep", Content: "out"},
		{Role: llm.RoleUser, Content: "more"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_drop", Name: "write"}}},
	}
	st := newRewindState(t, mgr, fs, "s1", msgs)
	dir := st.GetPersistedSessionDir()

	// Seed the artifacts a rewind must clear.
	_ = os.WriteFile(filepath.Join(dir, "branches.json"), []byte(`{"version":1}`), 0o644)
	if err := os.MkdirAll(filepath.Join(dir, "diffs"), 0o755); err != nil {
		t.Fatalf("diffs dir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "diffs", "turn_2.json"), []byte(`{}`), 0o644)
	if err := WriteToolCallResult(dir, "call_keep", "out"); err != nil {
		t.Fatalf("tool call keep: %v", err)
	}
	if err := WriteToolCallResult(dir, "call_drop", "out"); err != nil {
		t.Fatalf("tool call drop: %v", err)
	}
	if err := WritePendingPermission(dir, acp.PermissionRequestParams{
		SessionID: "s1",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_drop"},
	}, "write", "{}"); err != nil {
		t.Fatalf("pending permission: %v", err)
	}

	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "branches.json")); !os.IsNotExist(err) {
		t.Fatalf("branches.json should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "diffs")); !os.IsNotExist(err) {
		t.Fatalf("diffs/ should be gone: %v", err)
	}
	if PendingPermissionHeld(dir) {
		t.Fatalf("pending_permission.json for a dropped call should be cleared")
	}
	dirs, err := ListToolCalls(dir)
	if err != nil {
		t.Fatalf("ListToolCalls: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != ToolCallDirName("call_keep") {
		t.Fatalf("expected only call_keep tool_call dir, got %v", dirs)
	}
}

func TestRewindKeepsPendingPermissionOfSurvivingCall(t *testing.T) {
	mgr, fs := newTestManager(t)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "ask"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_keep", Name: "read"}}},
		{Role: llm.RoleUser, Content: "more"},
	}
	st := newRewindState(t, mgr, fs, "s1", msgs)
	dir := st.GetPersistedSessionDir()
	if err := WritePendingPermission(dir, acp.PermissionRequestParams{
		SessionID: "s1",
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_keep"},
	}, "read", "{}"); err != nil {
		t.Fatalf("pending permission: %v", err)
	}

	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	if !PendingPermissionHeld(dir) {
		t.Fatalf("pending_permission.json for a surviving call should stay")
	}
}

func TestRewindThenAppendContinuesInPlace(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))

	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "edited world"})
	msgs := st.GetMessages()
	if len(msgs) != 3 || msgs[2].Content != "edited world" {
		t.Fatalf("expected the edited message at the rewind point, got %+v", msgs)
	}
}

func TestRewindPersistedToDisk(t *testing.T) {
	mgr, fs := newTestManager(t)
	st := newRewindState(t, mgr, fs, "s1", userMsgs("hello", "world"))
	st.SetPersistHook(func() { _ = fs.Save(st) })

	if _, err := mgr.RewindSession("s1", 1); err != nil {
		t.Fatalf("RewindSession: %v", err)
	}
	snap, err := fs.ReadSnapshot("s1")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(snap.Messages))
	}
}
