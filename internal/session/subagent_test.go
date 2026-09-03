package session_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// acpToLLM turns prompt blocks into the user message a turn appends.
func acpToLLM(blocks []acp.ContentBlock) llm.Message {
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return llm.Message{Role: llm.RoleUser, Content: b.String()}
}

// acpMCPToConfig is a stdio declaration shaped like the ones session/new carries.
func acpMCPToConfig(name string) config.MCPServerConfig {
	return config.MCPServerConfig{Name: name, Command: "mcp-" + name}
}

// holdHandle is a pool handle that stays running until stopped, standing in
// for a child agent run whose lifetime the pool controls.
type holdHandle struct {
	done chan struct{}
	once sync.Once
}

func newHoldHandle() *holdHandle { return &holdHandle{done: make(chan struct{})} }

func (h *holdHandle) Wait() (int, error) { <-h.done; return 0, nil }
func (h *holdHandle) Stop(time.Duration) error {
	h.once.Do(func() { close(h.done) })
	return nil
}
func (h *holdHandle) PID() int                    { return 0 }
func (h *holdHandle) ProcessStartedAt() time.Time { return time.Time{} }

type nopRunner struct{}

func (nopRunner) Start(bgtask.Spec, io.Writer) (bgtask.Handle, error) {
	return nil, errors.New("command runner must not be used by these tests")
}

func newSubagentTestManager(t *testing.T) (*session.Manager, *session.FileStore, string) {
	t.Helper()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(root, "home")
	var runs []string
	var mu sync.Mutex
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		mu.Lock()
		runs = append(runs, st.ID)
		mu.Unlock()
		st.AddMessage(acpToLLM(prompt))
		return string(acp.StopReasonEndTurn), nil
	}
	m := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		_ = runs
	})
	return m, store, root
}

func newParent(t *testing.T, m *session.Manager, cwd string) *session.State {
	t.Helper()
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	if st == nil {
		t.Fatal("parent session not registered")
	}
	return st
}

func TestNewSubagentSessionIDIsAValidFolderName(t *testing.T) {
	id := session.NewSubagentSessionID()
	if !strings.HasPrefix(id, "sub_") {
		t.Fatalf("id %q must carry the sub_ prefix", id)
	}
	if err := session.ValidateFolderSessionID(id); err != nil {
		t.Fatal(err)
	}
	if session.NewSubagentSessionID() == id {
		t.Fatal("ids must be unique")
	}
}

func TestCreateSubagentSessionRegistersPersistsAndLinksToTheParent(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)

	childID := session.NewSubagentSessionID()
	child, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID:              childID,
		ParentSessionID: parent.ID,
		Name:            "reviewer",
		TaskID:          "bg_7",
		CWD:             root,
		Mode:            "plan",
		PermissionMode:  "ask",
		SelectedModelID: "p2/gpt-4o-mini",
		Title:           "reviewer: check the diff",
		Role:            "You review code.",
		Tools:           []string{"read", "grep"},
		Depth:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.SessionByID(childID) != child {
		t.Fatal("the child must be the live session under its id")
	}
	meta := child.Subagent()
	if meta == nil || meta.ParentSessionID != parent.ID || meta.Name != "reviewer" || meta.TaskID != "bg_7" || meta.Depth != 1 {
		t.Fatalf("subagent meta = %+v", meta)
	}
	if meta.Role != "You review code." || strings.Join(meta.Tools, ",") != "read,grep" {
		t.Fatalf("role or tools lost: %+v", meta)
	}
	if child.GetMode() != "plan" || child.GetPermissionMode() != "ask" || child.GetSelectedModelID() != "p2/gpt-4o-mini" {
		t.Fatalf("child settings = %q %q %q", child.GetMode(), child.GetPermissionMode(), child.GetSelectedModelID())
	}
	if !child.IsSubagentRun() {
		t.Fatal("IsSubagentRun must be true for a child")
	}

	snap, err := store.ReadSnapshot(childID)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Meta.SubagentRun || snap.Meta.ParentSessionID != parent.ID || snap.Meta.SubagentName != "reviewer" || snap.Meta.SubagentTaskID != "bg_7" {
		t.Fatalf("persisted meta = %+v", snap.Meta)
	}
	if snap.Meta.TitlePinned != "reviewer: check the diff" {
		t.Fatalf("child title = %q", snap.Meta.TitlePinned)
	}
	if !snap.Meta.IsSubagentRun(childID) {
		t.Fatal("IsSubagentRun on the meta must be true")
	}
}

func TestSubagentSessionsStayOutOfTheListUnlessAsked(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SessionID == childID {
			t.Fatal("a subagent session must not appear in the default list")
		}
	}
	rows, err = store.ListSnapshotsWith(session.ListOptions{IncludeSubagents: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.SessionID == childID {
			found = true
		}
	}
	if !found {
		t.Fatal("include_subagents must list the child")
	}
}

func TestSubagentTurnRunsWhileExternalPromptsAreRefused(t *testing.T) {
	m, _, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	child, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	})
	if err != nil {
		t.Fatal(err)
	}

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the work"}}
	if _, err := m.RunSubagentTurn(context.Background(), childID, prompt, noopSender{}); err != nil {
		t.Fatalf("the runtime's own turn must run: %v", err)
	}
	if got := len(child.GetMessages()); got == 0 {
		t.Fatal("the child turn must have appended to the transcript")
	}

	_, err = m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: childID, Prompt: prompt})
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("external prompt error = %v, want ErrSubagentReadOnly", err)
	}
	_, err = m.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: childID, Prompt: prompt}, noopSender{}, &session.PromptRunOpts{DetachFromRequest: true})
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("HTTP-style prompt error = %v, want ErrSubagentReadOnly", err)
	}
	if _, err := m.RunPlan(context.Background(), childID, "any", noopSender{}); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("RunPlan error = %v, want ErrSubagentReadOnly", err)
	}
	// A normal session is unaffected by the guard.
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: parent.ID, Prompt: prompt}); err != nil {
		t.Fatalf("parent prompt must still run: %v", err)
	}
}

func TestRetireSubagentSessionDropsTheLiveEntryAndKeepsTheBundle(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}
	m.RetireSubagentSession(childID)
	if m.SessionByID(childID) != nil {
		t.Fatal("retired child must leave the live map")
	}
	if !store.HasPersistedSnapshot(childID) {
		t.Fatal("the bundle must stay on disk")
	}
	// Loading the finished bundle restores the subagent meta, so the read-only
	// guard still holds for a transcript served from disk.
	if _, err := m.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: childID, CWD: root}); err != nil {
		t.Fatal(err)
	}
	restored := m.SessionByID(childID)
	if restored == nil || !restored.IsSubagentRun() || restored.Subagent().ParentSessionID != parent.ID {
		t.Fatalf("restored child lost its subagent meta: %+v", restored.Subagent())
	}
	_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: childID, Prompt: []acp.ContentBlock{{Type: "text", Text: "x"}}})
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("restored child must stay read-only, got %v", err)
	}
}

func TestDeleteSessionTreeStopsRepresentingTasksBeforeRemovingBundles(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	// child under the parent, grandchild under the child; each represented by
	// a running pool task registered under its own parent session.
	childID := session.NewSubagentSessionID()
	childHandle := newHoldHandle()
	childTask, err := pool.Launch(bgtask.Spec{SessionID: parent.ID, Kind: bgtask.KindAgent, Agent: &bgtask.AgentInfo{Name: "mid", SessionID: childID}},
		func(string, io.Writer) (bgtask.Handle, error) { return childHandle, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "mid", TaskID: childTask.ID, CWD: root, Depth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	grandID := session.NewSubagentSessionID()
	grandHandle := newHoldHandle()
	grandTask, err := pool.Launch(bgtask.Spec{SessionID: childID, Kind: bgtask.KindAgent, Agent: &bgtask.AgentInfo{Name: "leaf", SessionID: grandID}},
		func(string, io.Writer) (bgtask.Handle, error) { return grandHandle, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: grandID, ParentSessionID: childID, Name: "leaf", TaskID: grandTask.ID, CWD: root, Depth: 2,
	}); err != nil {
		t.Fatal(err)
	}
	// A plain background command the grandchild left running.
	cmdHandle := newHoldHandle()
	cmdTask, err := pool.Launch(bgtask.Spec{SessionID: grandID, Kind: bgtask.KindCommand, Command: "sleep 60"},
		func(string, io.Writer) (bgtask.Handle, error) { return cmdHandle, nil })
	if err != nil {
		t.Fatal(err)
	}

	tree, err := m.SessionTree(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 3 || tree[0].ID != parent.ID || tree[1].ID != childID || tree[2].ID != grandID {
		t.Fatalf("tree order = %+v, want root, child, grandchild", tree)
	}

	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent.ID, childID, grandID} {
		if store.HasPersistedSnapshot(id) {
			t.Fatalf("bundle %s must be removed", id)
		}
		if m.SessionByID(id) != nil {
			t.Fatalf("session %s must leave the live map", id)
		}
	}
	for _, probe := range []struct {
		session, task string
	}{{parent.ID, childTask.ID}, {childID, grandTask.ID}, {grandID, cmdTask.ID}} {
		snap, err := pool.Get(probe.session, probe.task)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Status != bgtask.StatusStopped {
			t.Fatalf("task %s of %s = %q, want stopped", probe.task, probe.session, snap.Status)
		}
	}
	// Nothing writes into a removed bundle afterwards.
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(store.SessionPath(grandID)); !os.IsNotExist(err) {
		t.Fatalf("removed bundle reappeared: %v", err)
	}
}

func TestDeleteSessionTreeOnAChildStopsItsOwnTaskFirst(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	childID := session.NewSubagentSessionID()
	handle := newHoldHandle()
	task, err := pool.Launch(bgtask.Spec{SessionID: parent.ID, Kind: bgtask.KindAgent, Agent: &bgtask.AgentInfo{Name: "solo", SessionID: childID}},
		func(string, io.Writer) (bgtask.Handle, error) { return handle, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "solo", TaskID: task.ID, CWD: root, Depth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteSessionTree(childID, pool); err != nil {
		t.Fatal(err)
	}
	snap, err := pool.Get(parent.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != bgtask.StatusStopped {
		t.Fatalf("the child's representing task must be stopped, got %q", snap.Status)
	}
	if store.HasPersistedSnapshot(childID) {
		t.Fatal("child bundle must be removed")
	}
	if !store.HasPersistedSnapshot(parent.ID) || m.SessionByID(parent.ID) == nil {
		t.Fatal("the parent must be untouched")
	}
}

func TestSessionMCPDeclarationsAreRetainedForChildren(t *testing.T) {
	st := &session.State{ID: "s"}
	st.RememberSessionMCPDeclaration(acpMCPToConfig("alpha"))
	st.RememberSessionMCPDeclaration(acpMCPToConfig("beta"))
	decls := st.SessionMCPDeclarations()
	if len(decls) != 2 || decls[0].Name != "alpha" || decls[1].Name != "beta" {
		t.Fatalf("declarations = %+v", decls)
	}
	decls[0].Name = "mutated"
	if st.SessionMCPDeclarations()[0].Name != "alpha" {
		t.Fatal("SessionMCPDeclarations must return a copy")
	}
}
