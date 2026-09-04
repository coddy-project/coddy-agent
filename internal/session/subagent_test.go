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
	"sync/atomic"
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
	// onStop, when set, observes the world at the moment the pool stops the
	// handle.
	onStop func()
}

func newHoldHandle() *holdHandle { return &holdHandle{done: make(chan struct{})} }

func (h *holdHandle) Wait() (int, error) { <-h.done; return 0, nil }
func (h *holdHandle) Stop(time.Duration) error {
	h.once.Do(func() {
		if h.onStop != nil {
			h.onStop()
		}
		close(h.done)
	})
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

// newSubagentTestManagerWithRunner is newSubagentTestManager with a caller
// supplied runner, for tests that need a turn to block or to observe its
// prompt.
func newSubagentTestManagerWithRunner(t *testing.T, runner session.AgentRunner) (*session.Manager, *session.FileStore, string) {
	t.Helper()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(root, "home")
	m := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
	return m, store, root
}

// A delete that lands while the parent is mid-turn must cancel that turn and
// wait for it, refuse a turn that arrives meanwhile, and leave no bundle
// behind once the turn's persist hook has fired for the last time.
func TestDeleteSessionTreeCancelsAnActiveParentTurn(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(acpToLLM(prompt))
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		// A turn that is torn down still persists on its way out, as the
		// real loop does; the bundle must not come back from this write.
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "partial"})
		return string(acp.StopReasonCancelled), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	turnDone := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "long task"}},
		})
		turnDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the parent turn never started")
	}
	if !m.SessionTurnActiveInProcess(parent.ID) {
		t.Fatal("precondition: the parent turn must be active")
	}

	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	select {
	case <-turnDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled turn did not return")
	}
	if m.SessionTurnActiveInProcess(parent.ID) {
		t.Fatal("the turn must have settled before the delete returned")
	}
	if m.SessionByID(parent.ID) != nil {
		t.Fatal("the session must leave the live map")
	}
	time.Sleep(150 * time.Millisecond)
	if store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("the bundle must stay removed after the cancelled turn persisted on its way out")
	}
	if _, err := os.Stat(store.SessionPath(parent.ID)); !os.IsNotExist(err) {
		t.Fatalf("removed bundle reappeared: %v", err)
	}
}

// While a tree is being deleted, a prompt against any of its nodes is refused
// instead of recreating the bundle.
func TestPromptDuringDeleteIsRefused(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	var enteredOnce sync.Once
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(acpToLLM(prompt))
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
		case <-release:
		}
		return string(acp.StopReasonEndTurn), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}

	go func() {
		_, _ = m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: parent.ID, Prompt: prompt})
	}()
	<-entered

	// The delete blocks in cancelAndAwaitTurns only until the runner sees
	// ctx.Done, which it does at once; the interesting window is the one
	// between the mark and the removal, so a concurrent prompt fired right
	// after the delete starts must see ErrSessionDeleting or, if it lost the
	// race entirely, an unknown session, never a fresh bundle.
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	var promptErr error
	for i := 0; i < 200; i++ {
		_, promptErr = m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: parent.ID, Prompt: prompt})
		if promptErr != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	close(release)
	if promptErr == nil {
		t.Fatal("a prompt racing the delete must be refused")
	}
	if errors.Is(promptErr, session.ErrSessionDeleting) {
		return
	}
	// Lost the race: the session is gone and the prompt did not resurrect it.
	if store.HasPersistedSnapshot(parent.ID) {
		t.Fatalf("prompt error = %v and the bundle exists again", promptErr)
	}
}

// The order of the tree delete is observed at the moment each task is
// stopped, not inferred afterwards: when a node's representing task stops,
// its bundle and every bundle below it are still on disk.
func TestDeleteSessionTreeStopsTasksWhileBundlesStillExist(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	childID := session.NewSubagentSessionID()
	grandID := session.NewSubagentSessionID()
	type seen struct {
		task            string
		parentB, childB bool
		grandB          bool
	}
	var mu sync.Mutex
	var observations []seen
	observe := func(task string) func() {
		return func() {
			mu.Lock()
			observations = append(observations, seen{
				task:    task,
				parentB: store.HasPersistedSnapshot(parent.ID),
				childB:  store.HasPersistedSnapshot(childID),
				grandB:  store.HasPersistedSnapshot(grandID),
			})
			mu.Unlock()
		}
	}
	childHandle := newHoldHandle()
	childHandle.onStop = observe("child")
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
	grandHandle := newHoldHandle()
	grandHandle.onStop = observe("grand")
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

	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != 2 || observations[0].task != "child" || observations[1].task != "grand" {
		t.Fatalf("stop order = %+v, want the child's task first, then the grandchild's", observations)
	}
	for _, o := range observations {
		if !o.parentB || !o.childB || !o.grandB {
			t.Fatalf("task %s was stopped after a bundle was already gone: %+v", o.task, o)
		}
	}
}

// The HTTP surface may not mint an ordinary session under the sub_ prefix,
// and a child transcript cannot be forked into a writable branch.
func TestReservedPrefixAndBranchRefusals(t *testing.T) {
	m, _, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)

	_, err := m.EnsureHTTPSession(context.Background(), "sub_0123456789abcdef", root)
	if !errors.Is(err, session.ErrReservedSessionID) {
		t.Fatalf("EnsureHTTPSession on an unknown sub_ id = %v, want ErrReservedSessionID", err)
	}
	if m.SessionByID("sub_0123456789abcdef") != nil {
		t.Fatal("no session may be created under the reserved prefix")
	}

	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the work"}}
	if _, err := m.RunSubagentTurn(context.Background(), childID, prompt, noopSender{}); err != nil {
		t.Fatal(err)
	}
	// An existing child is served, not refused: the prefix guard is about
	// creating new sessions only.
	if st, err := m.EnsureHTTPSession(context.Background(), childID, root); err != nil || st == nil || st.ID != childID {
		t.Fatalf("EnsureHTTPSession on a live child = %v, %v", st, err)
	}
	_, err = m.CreateBranchSession(session.CreateBranchParams{SourceSessionID: childID, UserMessageIndex: 0})
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("branching a child = %v, want ErrSubagentReadOnly", err)
	}
	// Retired children are read from the bundle and refused the same way.
	m.RetireSubagentSession(childID)
	_, err = m.CreateBranchSession(session.CreateBranchParams{SourceSessionID: childID, UserMessageIndex: 0})
	if !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("branching a retired child = %v, want ErrSubagentReadOnly", err)
	}
}

// The child's task prompt is the parent model's text: the run-plan
// delegation and the @plans mention hydration that operator prompts get must
// not reinterpret it, or a task phrased "implement the plan x" would refuse
// the child's only turn.
func TestSubagentTurnTakesThePromptVerbatim(t *testing.T) {
	var got []string
	var mu sync.Mutex
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		mu.Lock()
		got = append(got, acpToLLM(prompt).Content)
		mu.Unlock()
		st.AddMessage(acpToLLM(prompt))
		return string(acp.StopReasonEndTurn), nil
	}
	m, _, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"implement the plan nonexistent-plan and report",
		"read @plans/nonexistent-plan.plan.md and summarise it",
	} {
		prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}
		if _, err := m.RunSubagentTurn(context.Background(), childID, prompt, noopSender{}); err != nil {
			t.Fatalf("child turn %q: %v", text, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || !strings.Contains(got[0], "implement the plan nonexistent-plan") || !strings.Contains(got[1], "@plans/nonexistent-plan.plan.md") {
		t.Fatalf("runner saw %q, want both prompts verbatim", got)
	}
}

// ---- review round 2: publication, admission and rollback ----

// While a child is being created its live entry is already published; every
// reader that finds it must see a read-only child, never an ordinary session
// that becomes one later.
func TestSubagentSessionIsReadOnlyFromTheMomentItIsPublished(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	published := make(chan struct{})
	release := make(chan struct{})
	m.SetSubagentPublishHookForTest(func(*session.State) {
		close(published)
		<-release
	})
	defer m.SetSubagentPublishHookForTest(nil)

	done := make(chan error, 1)
	go func() {
		_, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		done <- err
	}()
	select {
	case <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("the child was never published")
	}
	// Paused between the publish and the bundle: the live entry is the child.
	if store.HasPersistedSnapshot(childID) {
		t.Fatal("precondition: the bundle must not exist yet")
	}
	st, err := m.EnsureHTTPSession(context.Background(), childID, root)
	if err != nil || st == nil {
		t.Fatalf("EnsureHTTPSession during creation = %v, %v", st, err)
	}
	if !st.IsSubagentRun() || st.Subagent().ParentSessionID != parent.ID {
		t.Fatalf("the published state lacks its child metadata: %+v", st.Subagent())
	}
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "sneak in"}}
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: childID, Prompt: prompt}); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("prompt during creation = %v, want ErrSubagentReadOnly", err)
	}
	if _, err := m.RunPlan(context.Background(), childID, "any", noopSender{}); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("RunPlan during creation = %v, want ErrSubagentReadOnly", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !store.HasPersistedSnapshot(childID) {
		t.Fatal("the bundle must exist once creation returned")
	}
}

// A prompt that passed the first deleting check before DeleteSessionTree
// marked the session is still refused: it rechecks after installing its
// cancel, and the delete cancels whatever it finds installed.
func TestDeleteSessionTreeRefusesATurnAdmittedBeforeTheMark(t *testing.T) {
	var runs int32
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		atomic.AddInt32(&runs, 1)
		st.AddMessage(acpToLLM(prompt))
		return string(acp.StopReasonEndTurn), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	admitted := make(chan struct{})
	release := make(chan struct{})
	m.SetTurnAdmissionHookForTest(func(string) {
		close(admitted)
		<-release
	})
	defer m.SetTurnAdmissionHookForTest(nil)

	promptDone := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "late"}},
		})
		promptDone <- err
	}()
	select {
	case <-admitted:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never reached admission")
	}

	// The delete starts while the turn is paused between its first check and
	// its recheck; it marks, cancels and waits for the turn to release.
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-deleteDone:
		t.Fatalf("the delete returned %v while the admitted turn was still registered", err)
	default:
	}
	close(release)
	if err := <-promptDone; !errors.Is(err, session.ErrSessionDeleting) {
		t.Fatalf("admitted turn = %v, want ErrSessionDeleting", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&runs) != 0 {
		t.Fatal("the runner ran for a turn admitted against a deletion")
	}
	time.Sleep(100 * time.Millisecond)
	if store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("the bundle came back after the refused turn")
	}
}

// A runner that ignores its context aborts the delete instead of having its
// bundle pulled out from under it.
func TestDeleteSessionTreeAbortsWhenATurnIgnoresCancellation(t *testing.T) {
	restore := session.SetDeleteSettleTimeoutForTest(200 * time.Millisecond)
	defer restore()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(acpToLLM(prompt))
		once.Do(func() { close(entered) })
		<-release // deliberately deaf to ctx
		return string(acp.StopReasonEndTurn), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	promptDone := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "stubborn"}},
		})
		promptDone <- err
	}()
	<-entered

	err := m.DeleteSessionTree(parent.ID, pool)
	if !errors.Is(err, session.ErrTurnNotSettled) {
		t.Fatalf("delete with a deaf runner = %v, want ErrTurnNotSettled", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) || m.SessionByID(parent.ID) == nil {
		t.Fatal("an aborted delete must leave the session and its bundle in place")
	}
	// The mark is lifted again: once the turn ends the session is usable and
	// deletable.
	close(release)
	if err := <-promptDone; err != nil {
		t.Fatalf("the stubborn turn ended with %v", err)
	}
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "again"}},
	}); err != nil {
		t.Fatalf("prompt after an aborted delete = %v", err)
	}
	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatalf("second delete = %v", err)
	}
}

// A creation that fails or is cancelled leaves nothing behind: no live entry
// and no half-written sub_ bundle.
func TestCreateSubagentSessionRollsBackOnFailure(t *testing.T) {
	t.Run("cancelled before publish", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		parent := newParent(t, m, root)
		childID := session.NewSubagentSessionID()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := m.CreateSubagentSession(ctx, session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want a cancellation", err)
		}
		if m.SessionByID(childID) != nil || store.HasPersistedSnapshot(childID) {
			t.Fatal("a cancelled creation left a live entry or a bundle")
		}
	})
	t.Run("cancelled after publish", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		parent := newParent(t, m, root)
		childID := session.NewSubagentSessionID()
		ctx, cancel := context.WithCancel(context.Background())
		m.SetSubagentPublishHookForTest(func(*session.State) { cancel() })
		defer m.SetSubagentPublishHookForTest(nil)
		_, err := m.CreateSubagentSession(ctx, session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want a cancellation", err)
		}
		if m.SessionByID(childID) != nil {
			t.Fatal("the live entry must be rolled back")
		}
		if _, statErr := os.Stat(store.SessionPath(childID)); !os.IsNotExist(statErr) {
			t.Fatalf("the bundle created before the cancellation must be removed: %v", statErr)
		}
	})
	t.Run("layout failure", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		parent := newParent(t, m, root)
		childID := session.NewSubagentSessionID()
		// A file where the bundle directory should go makes EnsureLayout fail.
		if err := os.WriteFile(store.SessionPath(childID), []byte("in the way"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		if err == nil || !strings.Contains(err.Error(), "layout") {
			t.Fatalf("error = %v, want a layout failure", err)
		}
		if m.SessionByID(childID) != nil {
			t.Fatal("the live entry must be rolled back")
		}
		// Something that was there before the call is not ours to remove.
		if _, statErr := os.Stat(store.SessionPath(childID)); statErr != nil {
			t.Fatalf("a pre-existing path was removed: %v", statErr)
		}
	})
}

// ---- review round 3: shared admission and the tree-scan race ----

// A direct plan run is admitted like a prompt: paused after installing its
// cancel, a concurrent delete waits for it and it ends refused.
func TestRunPlanAdmissionRacesDeletion(t *testing.T) {
	var runs int32
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		atomic.AddInt32(&runs, 1)
		return string(acp.StopReasonEndTurn), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	admitted := make(chan struct{})
	release := make(chan struct{})
	m.SetTurnAdmissionHookForTest(func(string) {
		close(admitted)
		<-release
	})
	defer m.SetTurnAdmissionHookForTest(nil)

	planDone := make(chan error, 1)
	go func() {
		_, err := m.RunPlan(context.Background(), parent.ID, "any-plan", noopSender{})
		planDone <- err
	}()
	select {
	case <-admitted:
	case <-time.After(10 * time.Second):
		t.Fatal("RunPlan never reached admission")
	}
	if !m.SessionTurnActiveInProcess(parent.ID) {
		t.Fatal("a plan run must be registered as an active turn before it is admitted")
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-deleteDone:
		t.Fatalf("the delete returned %v while the plan run was still registered", err)
	default:
	}
	close(release)
	if err := <-planDone; !errors.Is(err, session.ErrSessionDeleting) {
		t.Fatalf("RunPlan = %v, want ErrSessionDeleting", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&runs) != 0 || store.HasPersistedSnapshot(parent.ID) {
		t.Fatalf("runs=%d bundle=%v after a refused plan run", runs, store.HasPersistedSnapshot(parent.ID))
	}
}

// BeginTurn is the path a caller-driven turn (the HTTP permission resume)
// uses: it installs a cancel the delete reaches, and it is refused once the
// session is marked.
func TestBeginTurnInstallsCancelAndRefusesDuringDeletion(t *testing.T) {
	m, _, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})

	turnCtx, finish, err := m.BeginTurn(context.Background(), parent.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.SessionTurnActiveInProcess(parent.ID) {
		t.Fatal("BeginTurn must register the turn")
	}
	if _, _, err := m.BeginTurn(context.Background(), parent.ID, nil); !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("second BeginTurn = %v, want ErrSessionTurnBusy", err)
	}
	parent.Cancel()
	select {
	case <-turnCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("State.Cancel did not cancel the admitted turn's context")
	}
	finish()
	if m.SessionTurnActiveInProcess(parent.ID) {
		t.Fatal("finish must unregister the turn")
	}

	// Paused after installing its cancel, a BeginTurn racing a delete ends
	// refused, exactly like a prompt.
	admitted := make(chan struct{})
	release := make(chan struct{})
	m.SetTurnAdmissionHookForTest(func(string) {
		close(admitted)
		<-release
	})
	defer m.SetTurnAdmissionHookForTest(nil)
	beginDone := make(chan error, 1)
	go func() {
		_, fin, err := m.BeginTurn(context.Background(), parent.ID, nil)
		if err == nil {
			fin()
		}
		beginDone <- err
	}()
	<-admitted
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-beginDone; !errors.Is(err, session.ErrSessionDeleting) {
		t.Fatalf("BeginTurn racing a delete = %v, want ErrSessionDeleting", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	// A child session is never admitted through BeginTurn either.
	other := newParent(t, m, root)
	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: other.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.BeginTurn(context.Background(), childID, nil); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("BeginTurn on a child = %v, want ErrSubagentReadOnly", err)
	}
}

// A child created and persisted after the delete's first snapshot is caught
// by the rescan and removed with the tree.
func TestDeleteSessionTreeCatchesAChildCreatedAfterTheFirstScan(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	scanned := make(chan struct{})
	release := make(chan struct{})
	m.SetTreeScanHookForTest(func(string) {
		close(scanned)
		<-release
	})
	defer m.SetTreeScanHookForTest(nil)

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	<-scanned

	// The delete holds its first, childless snapshot; a detached child is
	// created and persisted meanwhile, with its task in the pool.
	childID := session.NewSubagentSessionID()
	handle := newHoldHandle()
	task, err := pool.Launch(bgtask.Spec{SessionID: parent.ID, Kind: bgtask.KindAgent, Agent: &bgtask.AgentInfo{Name: "late", SessionID: childID}},
		func(string, io.Writer) (bgtask.Handle, error) { return handle, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "late", TaskID: task.ID, CWD: root, Depth: 1,
	}); err != nil {
		t.Fatalf("a child created before the mark must still be admitted: %v", err)
	}
	if !store.HasPersistedSnapshot(childID) {
		t.Fatal("precondition: the late child is persisted")
	}
	close(release)
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if store.HasPersistedSnapshot(childID) || m.SessionByID(childID) != nil {
		t.Fatal("the late child survived the delete as an orphan")
	}
	if snap, err := pool.Get(parent.ID, task.ID); err != nil || snap.Status != bgtask.StatusStopped {
		t.Fatalf("late child's task = %+v, %v, want stopped", snap, err)
	}
}

// A child whose creation is paused at publish while its parent is deleted
// ends refused and leaves no bundle behind, whether the delete finished or is
// still running when it resumes.
func TestCreateSubagentSessionRefusesWhenTheParentIsDeleted(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	childID := session.NewSubagentSessionID()
	published := make(chan struct{})
	release := make(chan struct{})
	m.SetSubagentPublishHookForTest(func(*session.State) {
		close(published)
		<-release
	})
	defer m.SetSubagentPublishHookForTest(nil)

	createDone := make(chan error, 1)
	go func() {
		_, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "paused", TaskID: "bg_1", CWD: root, Depth: 1,
		})
		createDone <- err
	}()
	<-published
	// The delete runs to completion while the creation is paused: the live
	// child is in its tree, so it is forgotten with the parent.
	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	close(release)
	err := <-createDone
	if !errors.Is(err, session.ErrSessionDeleting) {
		t.Fatalf("creation resumed after the delete = %v, want ErrSessionDeleting", err)
	}
	if m.SessionByID(childID) != nil {
		t.Fatal("the child must not be live")
	}
	if _, statErr := os.Stat(store.SessionPath(childID)); !os.IsNotExist(statErr) {
		t.Fatalf("the child bundle must not exist after the refused creation: %v", statErr)
	}
	if _, statErr := os.Stat(store.SessionPath(parent.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("the parent bundle must stay removed: %v", statErr)
	}

	// A parent that is not live at all refuses at once.
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: session.NewSubagentSessionID(), ParentSessionID: parent.ID, Name: "orphan", TaskID: "bg_2", CWD: root,
	}); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("creation under a gone parent = %v, want a not-live refusal", err)
	}
}

// ---- review round 4: stale states, counted marks, deep late generations ----

// A caller that resolved its state, then lost the race to a delete that ran
// to completion (mark raised and lowered again), holds a stale state: the
// turn is refused instead of running and recreating the removed bundle.
func TestTurnAdmissionRefusesAStaleStateAfterADeleteCompleted(t *testing.T) {
	var runs int32
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		atomic.AddInt32(&runs, 1)
		st.AddMessage(acpToLLM(prompt))
		return string(acp.StopReasonEndTurn), nil
	}
	m, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m.SetTurnEntryHookForTest(func(string) {
		once.Do(func() { close(entered) })
		<-release
	})
	defer m.SetTurnEntryHookForTest(nil)

	promptDone := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "stale"}},
		})
		promptDone <- err
	}()
	<-entered
	// Nothing is registered yet, so the delete does not wait: it removes the
	// session, forgets the live entry and lowers its mark.
	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	if m.IsDeletingForTest(parent.ID) {
		t.Fatal("precondition: the mark is lowered once the delete finished")
	}
	close(release)
	err := <-promptDone
	if !errors.Is(err, session.ErrSessionGone) {
		t.Fatalf("prompt on a stale state = %v, want ErrSessionGone", err)
	}
	if atomic.LoadInt32(&runs) != 0 {
		t.Fatal("the runner ran on a stale state")
	}
	time.Sleep(100 * time.Millisecond)
	if store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("the removed bundle came back")
	}
	if _, statErr := os.Stat(store.SessionPath(parent.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("removed bundle reappeared: %v", statErr)
	}
}

// BeginTurn refuses a stale state the same way, and a state replaced by a
// reload of the same id is stale too.
func TestBeginTurnRefusesAStaleState(t *testing.T) {
	m, _, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m.SetTurnEntryHookForTest(func(string) {
		once.Do(func() { close(entered) })
		<-release
	})
	defer m.SetTurnEntryHookForTest(nil)
	beginDone := make(chan error, 1)
	go func() {
		_, fin, err := m.BeginTurn(context.Background(), parent.ID, nil)
		if err == nil {
			fin()
		}
		beginDone <- err
	}()
	<-entered
	if err := m.DeleteSessionTree(parent.ID, pool); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-beginDone; !errors.Is(err, session.ErrSessionGone) {
		t.Fatalf("BeginTurn on a stale state = %v, want ErrSessionGone", err)
	}
}

// The deletion mark counts overlapping deletes: it stays raised until the
// last one lowers it.
func TestDeletionMarkIsCountedAcrossOverlappingDeletes(t *testing.T) {
	m, _, _ := newSubagentTestManager(t)
	ids := []string{"sess_a"}
	m.MarkDeletingForTest(ids, true)
	m.MarkDeletingForTest(ids, true)
	m.MarkDeletingForTest(ids, false)
	if !m.IsDeletingForTest("sess_a") {
		t.Fatal("the mark must survive the first of two overlapping deletes")
	}
	m.MarkDeletingForTest(ids, false)
	if m.IsDeletingForTest("sess_a") {
		t.Fatal("the mark must be lowered once the last delete finished")
	}
	m.MarkDeletingForTest(ids, false)
	if m.IsDeletingForTest("sess_a") {
		t.Fatal("lowering a mark that is not raised must stay a no-op")
	}
}

// Ten generations of children created after the delete's first snapshot are
// all found by the rescans and removed with the tree; none is orphaned.
func TestDeleteSessionTreeRemovesDeepGenerationsCreatedAfterTheFirstScan(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	pool := bgtask.NewWithRunner(bgtask.Config{}, nopRunner{})
	scanned := make(chan struct{})
	release := make(chan struct{})
	m.SetTreeScanHookForTest(func(string) {
		close(scanned)
		<-release
	})
	defer m.SetTreeScanHookForTest(nil)

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- m.DeleteSessionTree(parent.ID, pool) }()
	<-scanned

	const generations = 10
	ids := make([]string, 0, generations)
	prev := parent.ID
	for depth := 1; depth <= generations; depth++ {
		id := session.NewSubagentSessionID()
		if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: id, ParentSessionID: prev, Name: "gen", TaskID: "bg_" + id[4:8], CWD: root, Depth: depth,
		}); err != nil {
			t.Fatalf("generation %d: %v", depth, err)
		}
		ids = append(ids, id)
		prev = id
	}
	for _, id := range ids {
		if !store.HasPersistedSnapshot(id) {
			t.Fatalf("precondition: generation %s is persisted", id)
		}
	}
	close(release)
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if store.HasPersistedSnapshot(id) || m.SessionByID(id) != nil {
			t.Fatalf("generation %s survived the delete", id)
		}
	}
	if store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("the root must be removed")
	}
}

// ---- merge review: reserved prefix on session/new, read-only mode setters ----

// A client may not mint an ordinary session under the sub_ prefix through
// session/new either, and a child's mode, model and permission mode are fixed
// at spawn time on every surface.
func TestReservedPrefixOnSessionNewAndReadOnlyChildSettings(t *testing.T) {
	m, _, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)

	m.SetPreferredSessionID("sub_00000000000000000000beef")
	_, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if !errors.Is(err, session.ErrReservedSessionID) {
		t.Fatalf("session/new with a preferred sub_ id = %v, want ErrReservedSessionID", err)
	}
	if m.SessionByID("sub_00000000000000000000beef") != nil {
		t.Fatal("no session may exist under the reserved prefix")
	}

	childID := session.NewSubagentSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root, Mode: "plan",
	}); err != nil {
		t.Fatal(err)
	}
	child := m.SessionByID(childID)
	if err := m.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{SessionID: childID, ModeID: "agent"}); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("set_mode on a child = %v, want ErrSubagentReadOnly", err)
	}
	if _, err := m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: childID, ConfigID: "mode", Value: "agent"}); !errors.Is(err, session.ErrSubagentReadOnly) {
		t.Fatalf("set_config_option mode on a child = %v, want ErrSubagentReadOnly", err)
	}
	if got := child.GetMode(); got != "plan" {
		t.Fatalf("child mode = %q after refused switches, want plan", got)
	}
	// The parent still switches modes normally.
	if err := m.HandleSessionSetMode(context.Background(), acp.SessionSetModeParams{SessionID: parent.ID, ModeID: "ask"}); err != nil {
		t.Fatalf("set_mode on the parent = %v", err)
	}
	// A child may be created in ask mode (a read-only parent forces its own).
	askChild := session.NewSubagentSessionID()
	st, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID: askChild, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_2", CWD: root, Mode: "ask",
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.GetMode() != "ask" {
		t.Fatalf("ask child mode = %q", st.GetMode())
	}
}
