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

// A rewind before the first user message takes away the title that message
// derived, and its undo brings it back; the client hears both, since the
// listing it keeps follows the same title session/list reports.
func TestSessionInfoUpdateFollowsARewindAndItsUndo(t *testing.T) {
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
	if _, err := l.mgr.RewindSession(res.SessionID, 0); err != nil {
		t.Fatal(err)
	}
	if got := l.infoTitles(); len(got) != 2 || got[1] != "" {
		t.Fatalf("titles after rewinding the first message = %#v, want it cleared", got)
	}
	if _, err := l.mgr.UndoRewind(res.SessionID); err != nil {
		t.Fatal(err)
	}
	if got := l.infoTitles(); len(got) != 3 || got[2] != "Refactor the auth module" {
		t.Fatalf("titles after undoing the rewind = %#v, want the title back", got)
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

// ACP: deleting an already-deleted session, or one that never existed, should
// succeed silently. An editor retries a delete whose answer it lost, and the
// second one must not turn into an error dialog.
func TestSessionDeleteOfAnUnknownOrDeletedSessionSucceeds(t *testing.T) {
	l := newLifecycleManager(t)
	if _, err := l.mgr.HandleSessionDelete(context.Background(), acp.SessionDeleteParams{SessionID: "sess_0123456789abcdef01234567"}); err != nil {
		t.Fatalf("delete of a session that never existed: %v", err)
	}
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := l.mgr.HandleSessionDelete(context.Background(), acp.SessionDeleteParams{SessionID: res.SessionID}); err != nil {
			t.Fatalf("delete %d of %s: %v", i+1, res.SessionID, err)
		}
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

// A cold load or resume that restates the extra roots writes them to the
// bundle: a root the client took away stays away in session/list and when any
// surface opens the session again, instead of coming back inside the boundary
// of a confined turn. Restating the roots is bookkeeping about the session,
// not a change to the conversation, so it leaves the session where it was in a
// listing ordered by updatedAt; a request that restates the stored list does
// not rewrite the bundle at all.
func TestColdResumeWritesTheRootsItRestates(t *testing.T) {
	l := newLifecycleManager(t)
	extra := filepath.Join(l.root, "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root, AdditionalDirectories: []string{extra}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	row := func() acp.SessionListInfo {
		t.Helper()
		list, err := l.mgr.HandleSessionList(context.Background(), acp.SessionListParams{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range list.Sessions {
			if r.SessionID == res.SessionID {
				return r
			}
		}
		t.Fatalf("session %s not listed", res.SessionID)
		return acp.SessionListInfo{}
	}
	before := row()
	if before.UpdatedAt == nil {
		t.Fatal("listed session carries no updatedAt")
	}

	// The same list again: nothing to write.
	l.mgr.ForgetLiveSession(res.SessionID)
	metaPath := filepath.Join(l.root, "sessions", res.SessionID, "session.json")
	statBefore, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
		SessionID: res.SessionID, CWD: l.root, AdditionalDirectories: []string{extra},
	}); err != nil {
		t.Fatal(err)
	}
	if statAfter, err := os.Stat(metaPath); err != nil || !statAfter.ModTime().Equal(statBefore.ModTime()) {
		t.Fatalf("a resume restating the stored roots rewrote the bundle (err %v)", err)
	}

	// An empty list takes the root away, on disk too.
	l.mgr.ForgetLiveSession(res.SessionID)
	if _, err := l.mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
		SessionID: res.SessionID, CWD: l.root, AdditionalDirectories: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	after := row()
	if len(after.AdditionalDirectories) != 0 {
		t.Fatalf("session/list still reports the removed root: %#v", after.AdditionalDirectories)
	}
	if after.UpdatedAt == nil || *after.UpdatedAt != *before.UpdatedAt {
		t.Fatalf("restating the roots moved updatedAt from %v to %v", *before.UpdatedAt, after.UpdatedAt)
	}
	l.mgr.ForgetLiveSession(res.SessionID)
	if _, err := l.mgr.EnsureHTTPSession(context.Background(), res.SessionID, l.root); err != nil {
		t.Fatal(err)
	}
	if got := l.mgr.SessionByID(res.SessionID).GetAdditionalDirectories(); len(got) != 0 {
		t.Fatalf("the removed root came back on an internal reopen: %#v", got)
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

// The wire boundary refuses a relative additionalDirectory: ACP says each
// path is absolute, and one stored as-is would be read back by the workspace
// checks as a path against the turn's cwd - a different root than the client
// meant, or none at all.
func TestAdditionalDirectoriesMustBeAbsolute(t *testing.T) {
	l := newLifecycleManager(t)
	res, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		call func() error
	}{
		{"session/new", func() error {
			_, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
				CWD:                   l.root,
				AdditionalDirectories: []string{"relative"},
			})
			return err
		}},
		{"session/load", func() error {
			_, err := l.mgr.HandleSessionLoad(context.Background(), acp.SessionLoadParams{
				SessionID:             res.SessionID,
				CWD:                   l.root,
				AdditionalDirectories: []string{"relative"},
			})
			return err
		}},
		{"session/resume", func() error {
			_, err := l.mgr.HandleSessionResume(context.Background(), acp.SessionResumeParams{
				SessionID:             res.SessionID,
				CWD:                   l.root,
				AdditionalDirectories: []string{"relative"},
			})
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.call()
		if err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Fatalf("%s accepted a relative additional directory: %v", tc.name, err)
		}
	}
}

// A --session-id carry reopens the stored bundle without restating its scope:
// a session/new that omits additionalDirectories keeps the roots the bundle
// recorded, while one that sends a list - empty included - replaces them.
func TestPreferredSessionReopenKeepsRootsTheRequestDidNotRestate(t *testing.T) {
	l := newLifecycleManager(t)
	extra := filepath.Join(l.root, "carry-extra")
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
	if _, err := l.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}

	l.mgr.ForgetLiveSession(res.SessionID)
	l.mgr.SetPreferredSessionID(res.SessionID)
	if _, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: l.root}); err != nil {
		t.Fatal(err)
	}
	if got := l.mgr.SessionByID(res.SessionID).GetAdditionalDirectories(); len(got) != 1 || got[0] != extra {
		t.Fatalf("the carry wiped the stored roots: %#v", got)
	}

	l.mgr.ForgetLiveSession(res.SessionID)
	l.mgr.SetPreferredSessionID(res.SessionID)
	if _, err := l.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD:                   l.root,
		AdditionalDirectories: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if got := l.mgr.SessionByID(res.SessionID).GetAdditionalDirectories(); len(got) != 0 {
		t.Fatalf("a request that sent an empty list left %#v standing", got)
	}
}
