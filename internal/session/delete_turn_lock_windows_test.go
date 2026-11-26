package session_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestWindowsDeleteTreeRefusesActiveTurnThenRemovesBundle(t *testing.T) {
	entered := make(chan struct{})
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(entered)
		<-ctx.Done()
		return string(acp.StopReasonCancelled), nil
	}
	owner, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, owner, root)
	other := session.NewManager(testConfig(), noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: store.Root})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = owner.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-entered:
	case <-done:
		t.Fatal("prompt exited before entering the runner")
	case <-time.After(10 * time.Second):
		t.Fatal("prompt did not enter the runner")
	}
	if err := other.DeleteSessionTree(parent.ID, nil); !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("delete while turn is active: want busy, got %v", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("delete removed the active session")
	}
	cancel()
	<-done
	if err := other.DeleteSessionTree(parent.ID, nil); err != nil {
		t.Fatalf("delete after turn exits: %v", err)
	}
	if _, err := os.Stat(store.SessionPath(parent.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("delete returned success without removing the bundle: %v", err)
	}
}
