package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A surface that has something to say about answering through it says it in the
// system prompt of that turn. The happy path of a chat turn carrying one is
// features/gateway_session_identity.feature; this is the block arriving.
func TestSurfaceSystemPromptReachesTheSystemPrompt(t *testing.T) {
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	a := NewAgent(cfg, st, nil, nil)

	const block = "## Answering in a Telegram chat\n\nNo `#` headings."
	if got := a.buildSystemPrompt("agent", nil, nil, nil); strings.Contains(got, block) {
		t.Fatal("a turn nobody spoke for carries no surface block")
	}

	st.SetSurfaceSystemPrompt(block)
	got := a.buildSystemPrompt("agent", nil, nil, nil)
	if !strings.Contains(got, block) {
		t.Fatalf("the surface block is missing from the system prompt:\n%s", got)
	}

	// It is the turn's, not the session's: cleared, the next prompt is the
	// one every other surface would have built.
	st.SetSurfaceSystemPrompt("")
	if after := a.buildSystemPrompt("agent", nil, nil, nil); strings.Contains(after, block) {
		t.Fatal("the block outlived the turn that contributed it")
	}
}

// The block is turn state, so it must never reach the bundle: a transcript
// that recorded where an answer was going would read differently depending on
// which surface happened to run the last turn.
func TestSurfaceSystemPromptIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	store := &session.FileStore{Root: dir}
	sessionDir, err := store.EnsureLayout("sess_surface")
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: "sess_surface", CWD: dir, Mode: session.ModeAgent, SessionDir: sessionDir}
	st.SetSurfaceSystemPrompt("## Answering in a Telegram chat\n\nNo `#` headings.")
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	snap, err := store.ReadSnapshot("sess_surface")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range snap.Messages {
		if strings.Contains(m.Content, "Telegram") {
			t.Fatalf("the surface block was persisted into the transcript: %q", m.Content)
		}
	}
	restored := &session.State{ID: "sess_surface", CWD: dir, Mode: session.ModeAgent}
	if got := restored.GetSurfaceSystemPrompt(); got != "" {
		t.Fatalf("a reloaded session carries a surface block: %q", got)
	}
}
