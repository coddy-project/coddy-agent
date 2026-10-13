package session_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// chainRun is one run of the chain harness's runner: the session it ran in,
// its prompt and the goal marker of its first message.
type chainRun struct {
	session string
	prompt  string
	marker  *llm.GoalTurn
}

// chainHarness drives goal chains: a real manager over a session store and a
// workspace, a scripted runner that records which session ran what, a judge
// that finds every goal met unless told otherwise, and a starter that runs the
// chained session's first turn through HandleSessionPromptWithSender, so it
// takes the real turn lock.
type chainHarness struct {
	t     *testing.T
	mgr   *session.Manager
	sid   string
	cwd   string
	store *session.FileStore

	mu      sync.Mutex
	runs    []chainRun
	onRun   func(st *session.State, run int)
	starts  []string
	started chan string
}

func newChainHarness(t *testing.T) *chainHarness {
	t.Helper()
	root := t.TempDir()
	h := &chainHarness{t: t, cwd: filepath.Join(root, "work"), started: make(chan string, 16)}
	h.store = &session.FileStore{Root: filepath.Join(root, "sessions")}
	for _, dir := range []string{h.cwd, h.store.Root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(root, "home")
	h.mgr = session.NewManager(cfg, noopSender{}, h.run, slog.Default(), h.cwd, h.store)
	h.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "all done"}, nil
	})
	h.mgr.SetGoalChainStarter(h.start)
	res, err := h.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: h.cwd})
	if err != nil {
		t.Fatal(err)
	}
	h.sid = res.SessionID
	return h
}

func (h *chainHarness) run(_ context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
	marker := st.TakeTurnGoal()
	text := ""
	if len(prompt) > 0 {
		text = prompt[0].Text
	}
	h.mu.Lock()
	n := len(h.runs)
	h.runs = append(h.runs, chainRun{session: st.ID, prompt: text, marker: marker})
	onRun := h.onRun
	h.mu.Unlock()
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text, GoalTurn: marker, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	if onRun != nil {
		onRun(st, n)
	}
	return toolTurn(st, snd, fmt.Sprintf(`{"path":"file%d.txt"}`, n), "ok "+fmt.Sprint(n)), nil
}

// start is the harness's chain starter: what coddy serve does, without the
// relay - the prompt through the manager under the real turn lock.
func (h *chainHarness) start(ctx context.Context, params acp.SessionPromptParams, opts *session.PromptRunOpts) error {
	h.mu.Lock()
	h.starts = append(h.starts, params.SessionID)
	h.mu.Unlock()
	_, err := h.mgr.HandleSessionPromptWithSender(ctx, params, noopSender{}, opts)
	h.started <- params.SessionID
	return err
}

// waitStarted waits for the chained first turn of the next session to end.
func (h *chainHarness) waitStarted() string {
	h.t.Helper()
	select {
	case id := <-h.started:
		return id
	case <-time.After(10 * time.Second):
		h.t.Fatal("no chained session ran its first turn")
		return ""
	}
}

// writeAt writes text to a path relative to the workspace.
func (h *chainHarness) writeAt(rel, text string) {
	h.t.Helper()
	p := filepath.Join(h.cwd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// writesInFirstTurn makes the worker of the first run write text to rel.
func (h *chainHarness) writesInFirstTurn(rel, text string) {
	h.mu.Lock()
	h.onRun = func(_ *session.State, run int) {
		if run == 0 {
			h.writeAt(rel, text)
		}
	}
	h.mu.Unlock()
}

func (h *chainHarness) prompt(sessionID, text string) *acp.SessionPromptResult {
	h.t.Helper()
	res, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sessionID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	})
	if err != nil {
		h.t.Fatalf("prompt %q: %v", text, err)
	}
	return res
}

func (h *chainHarness) st(id string) *session.State { return h.mgr.SessionByID(id) }

func (h *chainHarness) runsIn(id string) []chainRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []chainRun
	for _, r := range h.runs {
		if r.session == id {
			out = append(out, r)
		}
	}
	return out
}

func (h *chainHarness) sessionsRun() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range h.runs {
		if !seen[r.session] {
			seen[r.session] = true
			out = append(out, r.session)
		}
	}
	return out
}

func (h *chainHarness) startCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.starts)
}

// notices are the notice rows of a session's transcript.
func notices(st *session.State) []string {
	var out []string
	for _, e := range st.GetUILog() {
		if e.Level == session.UILogLevelNotice {
			out = append(out, e.Message)
		}
	}
	return out
}

func hasNotice(st *session.State, sub string) bool {
	for _, n := range notices(st) {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// startedCopies lists the consumed copies of rel next to it.
func (h *chainHarness) startedCopies(rel string) []string {
	h.t.Helper()
	p := filepath.Join(h.cwd, filepath.FromSlash(rel))
	ext := filepath.Ext(p)
	matches, err := filepath.Glob(strings.TrimSuffix(p, ext) + ".started-*" + ext)
	if err != nil {
		h.t.Fatal(err)
	}
	return matches
}

func (h *chainHarness) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(h.cwd, filepath.FromSlash(rel)))
	return err == nil
}
