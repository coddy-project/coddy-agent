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
