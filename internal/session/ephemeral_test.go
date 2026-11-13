package session

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func ephemeralManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: filepath.Join(root, "home"), CWD: root}}
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	return NewManager(cfg, mcpTestSender{}, runner, slog.Default(), root, &FileStore{Root: sessRoot}), sessRoot
}

func TestEphemeralStateLeavesNothingInTheManagerOrOnDisk(t *testing.T) {
	m, sessRoot := ephemeralManager(t)
	st := m.NewEphemeralState()
	st.ReplaceMessagesFromClient([]llm.Message{{Role: llm.RoleUser, Content: "the question"}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "the answer"})
	st.AppendUILogError(1, "a failed call")

	if got := st.GetMessages(); len(got) != 2 || got[1].Content != "the answer" {
		t.Fatalf("messages = %+v", got)
	}
	if st.GetPersistedSessionDir() != "" {
		t.Fatalf("session dir = %q", st.GetPersistedSessionDir())
	}
	if m.getSession(st.GetID()) != nil {
		t.Fatal("an ephemeral state is registered among the live sessions")
	}
	entries, err := os.ReadDir(sessRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the store holds %q", entries[0].Name())
	}
}

// Nothing else can reach the state, so the turn lock excludes nobody: a second
// acquire of the same state is granted where a stored session's would be busy.
func TestEphemeralStateTakesNoTurnLock(t *testing.T) {
	m, _ := ephemeralManager(t)
	st := m.NewEphemeralState()
	for range 2 {
		unlock, err := m.AcquireComposerTurnLock(st.GetID(), st)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer unlock()
	}

	stored, err := m.EnsureHTTPSession(context.Background(), NewSessionID(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := m.AcquireComposerTurnLock(stored.GetID(), stored)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := m.AcquireComposerTurnLock(stored.GetID(), stored); !errors.Is(err, ErrSessionTurnBusy) {
		t.Fatalf("a stored session's second acquire: %v, want busy", err)
	}
}
