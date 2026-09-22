package session

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func newLoadTestManager(t *testing.T, root string, fs *FileStore) *Manager {
	t.Helper()
	return NewManager(&config.Config{}, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), root, fs)
}

// A session that is already live must survive a load: rebuilding a second
// State for the same id puts two writers on one bundle, and the replacement's
// snapshot was read before the running turn's messages landed, so its saves
// overwrite the history the live state is still producing.
func TestSessionLoadKeepsLiveSession(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)

	dir, err := fs.EnsureLayout("s1")
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	live := &State{ID: "s1", CWD: root, SessionDir: dir}
	live.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	live.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	if err := fs.Save(live); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A message the turn produced but has not persisted yet - the window the
	// racing load used to read an empty snapshot over a live history.
	live.AddMessage(llm.Message{Role: llm.RoleUser, Content: "unflushed"})
	if _, ok := mgr.registerSession("s1", live); !ok {
		t.Fatalf("registerSession: live state did not win an empty map")
	}

	res, err := mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: "s1"})
	if err != nil {
		t.Fatalf("HandleSessionLoad: %v", err)
	}
	if res == nil {
		t.Fatalf("HandleSessionLoad returned no result")
	}
	if got := mgr.SessionByID("s1"); got != live {
		t.Fatalf("session/load replaced the live state")
	}
	if msgs := live.GetMessages(); len(msgs) != 3 {
		t.Fatalf("live history lost messages: %d left", len(msgs))
	}
}

// A cold load still reads the bundle, and a second load of the now-live
// session returns the same state instead of rebuilding it.
func TestSessionLoadColdReadsSnapshot(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)

	dir, err := fs.EnsureLayout("s1")
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	st := &State{ID: "s1", CWD: root, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	if err := fs.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: "s1"}); err != nil {
		t.Fatalf("HandleSessionLoad: %v", err)
	}
	got := mgr.SessionByID("s1")
	if got == nil || got == st {
		t.Fatalf("expected a freshly loaded state, got %v", got)
	}
	if msgs := got.GetMessages(); len(msgs) != 2 {
		t.Fatalf("expected 2 messages from the snapshot, got %d", len(msgs))
	}

	if _, err := mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: "s1"}); err != nil {
		t.Fatalf("second HandleSessionLoad: %v", err)
	}
	if mgr.SessionByID("s1") != got {
		t.Fatalf("a second load replaced the live state")
	}
}

// registerSession is compare-and-swap: the first writer keeps the slot, a
// loser is handed back so its caller can discard the duplicate state.
func TestRegisterSessionKeepsFirstWriter(t *testing.T) {
	mgr := newLoadTestManager(t, t.TempDir(), nil)
	a, b := &State{ID: "s1"}, &State{ID: "s1"}

	if w, ok := mgr.registerSession("s1", a); !ok || w != a {
		t.Fatalf("first registration must win")
	}
	if w, ok := mgr.registerSession("s1", b); ok || w != a {
		t.Fatalf("a second writer must not replace the live session")
	}
}
