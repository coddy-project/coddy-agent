//go:build unix

package session_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestDeleteTreeRefusesOtherManagerPrompt(t *testing.T) {
	entered := make(chan struct{})
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(entered)
		<-ctx.Done()
		return string(acp.StopReasonCancelled), nil
	}
	owner, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, owner, root)
	other := session.NewManager(testConfig(), noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: store.Root})
	if _, err := other.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: parent.ID, CWD: root}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = owner.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: parent.ID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("prompt did not start")
	}
	err := other.DeleteSessionTree(parent.ID, nil)
	if !errors.Is(err, session.ErrSessionTurnBusy) || !strings.Contains(err.Error(), parent.ID) {
		t.Fatalf("delete error = %v", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) {
		t.Fatal("active session removed")
	}
}

func TestDeleteTreeRefusesForeignChildTask(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	childID := session.NewSessionID()
	if _, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{ID: childID, ParentSessionID: parent.ID, Name: "child", CWD: root, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	pool.SetSessionDir(childID, store.SessionPath(childID))
	task, err := pool.Start(bgtask.Spec{SessionID: childID, Kind: bgtask.KindCommand, Command: "sleep 60"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Stop(childID, task.ID) })
	other := session.NewManager(testConfig(), noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: store.Root})
	err = other.DeleteSessionTree(parent.ID, nil)
	if !errors.Is(err, session.ErrForeignSessionTask) || !strings.Contains(err.Error(), childID) {
		t.Fatalf("delete error = %v", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) || !store.HasPersistedSnapshot(childID) {
		t.Fatal("tree partially removed")
	}
}
