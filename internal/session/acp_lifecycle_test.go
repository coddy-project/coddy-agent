package session_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// lifecycleManager is a manager over a throwaway store whose turns write one
// user message, which is where the title derived from a conversation comes
// from, and whose updates are captured instead of written to a client.
type lifecycleManager struct {
	mgr    *session.Manager
	sender *captureSender
	root   string
}

func newLifecycleManager(t *testing.T) *lifecycleManager {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(root, "home")
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	sender := &captureSender{}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var b strings.Builder
		for _, block := range prompt {
			b.WriteString(block.Text)
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(b.String())})
		return string(acp.StopReasonEndTurn), nil
	}
	return &lifecycleManager{
		mgr:    session.NewManager(cfg, sender, runner, testLogger(), root, store),
		sender: sender,
		root:   root,
	}
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func (l *lifecycleManager) infoTitles() []string {
	l.sender.mu.Lock()
	defer l.sender.mu.Unlock()
	out := make([]string, 0, len(l.sender.ups))
	for _, u := range l.sender.ups {
		upd, ok := u.(acp.SessionInfoUpdate)
		if !ok {
			continue
		}
		if upd.Title == nil {
			out = append(out, "")
			continue
		}
		out = append(out, *upd.Title)
	}
	return out
}

// Nothing reaches the client before the response naming the session is on the
// wire: a title the agent moves in that window is about a session the client
// has not registered yet, and an update for it would be dropped or misread.
func TestSessionInfoUpdateWaitsUntilTheClientKnowsTheSession(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	st := l.mgr.SessionByID(res.SessionID)
	if st == nil {
		t.Fatal("the new session is not live")
	}

	st.SetTitlePinned("Ship the release")
	if got := l.infoTitles(); len(got) != 0 {
		t.Fatalf("titles sent before the client knew the session: %#v", got)
	}

	l.mgr.HandleSessionReady(res.SessionID)
	got := l.infoTitles()
	if len(got) != 1 || got[0] != "Ship the release" {
		t.Fatalf("titles after ready = %#v, want the pinned one once", got)
	}
}

// The title a first message derives is announced once: the conversation is
// named, and a later message does not rename it.
func TestSessionInfoUpdateAnnouncesADerivedTitleOnce(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	l.mgr.HandleSessionReady(res.SessionID)

	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "Refactor the auth module"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := l.infoTitles()
	if len(got) != 1 || got[0] != "Refactor the auth module" {
		t.Fatalf("titles after the first message = %#v, want the derived one once", got)
	}

	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "and then the store"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := l.infoTitles(); len(got) != 1 {
		t.Fatalf("a second message renamed the conversation: %#v", got)
	}
}

// A title that is taken away again is announced as cleared rather than left
// standing on the client.
func TestSessionInfoUpdateClearsATitleThatIsRemoved(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	l.mgr.HandleSessionReady(res.SessionID)
	st := l.mgr.SessionByID(res.SessionID)

	st.SetTitlePinned("Ship the release")
	st.SetTitlePinned("")
	got := l.infoTitles()
	if len(got) != 2 || got[0] != "Ship the release" || got[1] != "" {
		t.Fatalf("titles = %#v, want the rename and then a clear", got)
	}
}

func TestSessionDeleteTakesTheBundleAndTheLiveSession(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.mgr.HandleSessionDelete(context.Background(), acp.SessionDeleteParams{SessionID: res.SessionID}); err != nil {
		t.Fatal(err)
	}
	if l.mgr.SessionByID(res.SessionID) != nil {
		t.Fatal("the deleted session is still live")
	}
	if _, err := os.Stat(filepath.Join(l.root, "sessions", res.SessionID)); !os.IsNotExist(err) {
		t.Fatalf("bundle still on disk: %v", err)
	}
}

func TestSessionDeleteRefusesWithoutAStore(t *testing.T) {
	cfg := testConfig()
	cfg.Paths.Home = t.TempDir()
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, testLogger(), t.TempDir(), nil)
	if _, err := mgr.HandleSessionDelete(context.Background(), acp.SessionDeleteParams{SessionID: "sess_x"}); err == nil {
		t.Fatal("session/delete answered without persistence")
	}
}

// A resume brings the session back without the transcript: the client keeps
// its own copy of the history and only needs to know how to drive the session.
func TestSessionResumeDoesNotReplayTheTranscript(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	l.mgr.HandleSessionReady(res.SessionID)
	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Cold, the way an editor resumes one after the agent went away.
	l.mgr.ForgetLiveSession(res.SessionID)
	l.sender.mu.Lock()
	l.sender.ups = nil
	l.sender.mu.Unlock()

	out, err := l.mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
		SessionID: res.SessionID,
		CWD:       l.root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Modes == nil || out.Modes.CurrentModeID == "" {
		t.Fatalf("resume named no mode: %#v", out)
	}
	l.sender.mu.Lock()
	defer l.sender.mu.Unlock()
	for _, u := range l.sender.ups {
		raw, err := json.Marshal(u)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), `"user_message_chunk"`) || strings.Contains(string(raw), `"agent_message_chunk"`) {
			t.Fatalf("session/resume replayed the transcript: %s", raw)
		}
	}
}

// The extra workspace roots of a wire request are the complete list: a request
// that names none has none, which is what a client that does not manage them
// gets. A surface that is merely reading the session keeps what the bundle
// recorded.
func TestAdditionalDirectoriesFollowTheRequestAndSurviveAnInternalRead(t *testing.T) {
	l := newLifecycleManager(t)
	extra := filepath.Join(l.root, "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD:                   l.root,
		AdditionalDirectories: []string{extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := l.mgr.SessionByID(res.SessionID)
	if got := st.GetAdditionalDirectories(); len(got) != 1 || got[0] != extra {
		t.Fatalf("additional directories = %#v, want %q", got, extra)
	}

	// Persisted and reported back through session/list.
	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := l.mgr.HandleSessionList(context.Background(), acp.SessionListParams{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range list.Sessions {
		if row.SessionID != res.SessionID {
			continue
		}
		found = true
		if len(row.AdditionalDirectories) != 1 || row.AdditionalDirectories[0] != extra {
			t.Fatalf("session/list reports %#v, want %q", row.AdditionalDirectories, extra)
		}
	}
	if !found {
		t.Fatalf("session %s not listed", res.SessionID)
	}

	// A surface reading the session keeps the roots the bundle recorded.
	l.mgr.ForgetLiveSession(res.SessionID)
	if _, err := l.mgr.EnsureHTTPSession(context.Background(), res.SessionID, l.root); err != nil {
		t.Fatal(err)
	}
	if got := l.mgr.SessionByID(res.SessionID).GetAdditionalDirectories(); len(got) != 1 || got[0] != extra {
		t.Fatalf("an internal read stripped the roots: %#v", got)
	}

	// A resume that names none replaces them with none.
	if _, err := l.mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
		SessionID: res.SessionID,
		CWD:       l.root,
	}); err != nil {
		t.Fatal(err)
	}
	if got := l.mgr.SessionByID(res.SessionID).GetAdditionalDirectories(); len(got) != 0 {
		t.Fatalf("a request that named no roots left %#v standing", got)
	}
}
