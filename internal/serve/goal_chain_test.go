package serve

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// recordingMirror records the sessions whose turns it was asked to mirror.
type recordingMirror struct {
	mu       sync.Mutex
	sessions []string
}

func (m *recordingMirror) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	m.mu.Lock()
	m.sessions = append(m.sessions, sessionID)
	m.mu.Unlock()
	return primary, func() {}
}

func (m *recordingMirror) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sessions...)
}

// coddy serve can run a session nobody opened, so /goal --then-file is taken
// there rather than refused.
func TestRuntimeTakesAGoalChain(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: cfg, NeedsSessions: true, SessionsRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	res, err := rt.Mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// No model is configured, so the kickoff turn fails; the goal was taken.
	_, _ = rt.Mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal --then-file next.md ship it"}},
	})
	if g := rt.Mgr.SessionByID(res.SessionID).GetGoal(); g.ThenFile != "next.md" {
		t.Fatalf("goal = %+v, want the follow-up file taken", g)
	}
}

// The first turn of a chained session takes the session's turn lock before
// anything is mirrored, so a turn already running there keeps its watchers;
// it is mirrored into the HTTP relay, and nobody being there to answer, a
// prompt gets the operator's standing answer.
func TestRuntimeRunsAChainedTurnUnderTheLockAndMirrorsIt(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: filepath.Join(root, "home"), CWD: root}}
	answers := make(chan *acp.PermissionResult, 2)
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		res, _ := snd.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: st.GetID(),
			ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: "Run: rm -rf build"},
		})
		answers <- res
		return string(acp.StopReasonEndTurn), nil
	}
	rt := &Runtime{Log: slog.Default(), Store: store}
	rt.live.replace(cfg)
	rt.Mgr = session.NewManager(cfg, &defaultSender{live: rt.Cfg}, runner, slog.Default(), root, store)
	mirror := &recordingMirror{}
	rt.SetTurnMirror(mirror)
	res, err := rt.Mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	params := acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}}}

	_, release, err := rt.Mgr.BeginSessionWork(context.Background(), res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.runGoalChainTurn(context.Background(), params, &session.PromptRunOpts{}); !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("a held session answered %v, want busy", err)
	}
	if got := mirror.seen(); len(got) != 0 {
		t.Fatalf("a refused turn was mirrored: %v", got)
	}
	release()

	if err := rt.runGoalChainTurn(context.Background(), params, &session.PromptRunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := mirror.seen(); len(got) != 1 || got[0] != res.SessionID {
		t.Fatalf("mirrored %v, want the chained session", got)
	}
	if got := <-answers; got == nil || got.OptionID != "reject" {
		t.Fatalf("a prompt nobody can see was answered %+v, want a refusal", got)
	}
}
