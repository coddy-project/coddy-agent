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
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestDeleteTreeRefusesOtherManagerChildPrompt(t *testing.T) {
	entered := make(chan struct{})
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(entered)
		<-ctx.Done()
		return string(acp.StopReasonCancelled), nil
	}
	owner, store, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, owner, root)
	child := session.NewSessionID()
	if _, err := owner.CreateSubagentSession(context.Background(), session.SubagentSpec{ID: child, ParentSessionID: parent.ID, Name: "child", CWD: root, Depth: 1}); err != nil {
		t.Fatal(err)
	}
	other := session.NewManager(testConfig(), noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: store.Root})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = owner.RunSubagentTurn(ctx, child, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}, noopSender{})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("child prompt did not start")
	}
	err := other.DeleteSessionTree(parent.ID, nil)
	if !errors.Is(err, session.ErrSessionTurnBusy) || !strings.Contains(err.Error(), child) {
		t.Fatalf("delete error = %v", err)
	}
	if !store.HasPersistedSnapshot(parent.ID) || !store.HasPersistedSnapshot(child) {
		t.Fatal("tree partially removed")
	}
}
