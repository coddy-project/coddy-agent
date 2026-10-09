package session

// The console writes a session only with its first prompt (issue #357): a
// session opened and left without a message leaves no folder, and what was
// chosen in it before that prompt - a model switched with /model - is written
// together with the prompt rather than lost.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestDeferredBundleKeepsASettingChosenBeforeTheFirstPrompt(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models: []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 200},
			{Model: "fake/model-two", MaxTokens: 200},
		},
		Agent: config.Agent{Model: "fake/model"},
	}
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	store := &FileStore{Root: filepath.Join(home, "sessions")}
	mgr := NewManager(cfg, mcpTurnSender{}, runner, slog.Default(), cwd, store)
	mgr.SetDeferNewSessionBundle(true)
	ctx := context.Background()

	created, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	sid := created.SessionID
	t.Cleanup(func() { mgr.ForgetLiveSession(sid) })
	noBundle := func(when string) {
		t.Helper()
		if store.HasPersistedSnapshot(sid) {
			t.Fatalf("%s: the session was written before its first prompt", when)
		}
		if entries, _ := os.ReadDir(store.Root); len(entries) != 0 {
			t.Fatalf("%s: the sessions folder holds %d entries", when, len(entries))
		}
	}
	noBundle("after the session started")

	prompt := func(text string) {
		t.Helper()
		if _, err := mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
			SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: text}},
		}, mcpTurnSender{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	prompt("/model fake/model-two")
	noBundle("after a settings command alone")
	if got := mgr.SessionByID(sid).GetSelectedModelID(); got != "fake/model-two" {
		t.Fatalf("selected model = %q, want fake/model-two", got)
	}

	prompt("first work")
	if mgr.SessionByID(sid).BundleDeferred() || !store.HasPersistedSnapshot(sid) {
		t.Fatal("the first prompt did not write the session")
	}
	meta, err := store.ReadMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SelectedModelID != "fake/model-two" {
		t.Fatalf("written model = %q, want the one chosen before the first prompt", meta.SelectedModelID)
	}
}
