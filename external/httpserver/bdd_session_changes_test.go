//go:build http

package httpserver

// Godog harness for features/session_changes.feature: drives the live
// /coddy/sessions/{id}/changes surface behind the changed-files card, from
// a real turn that edits the workspace through to rolling the session back.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type changedFileRow struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
	Patch     string `json:"patch"`
}

type sessionChangesState struct {
	root      string
	home      string
	workspace string
	sessRoot  string
	ts        *httptest.Server
	srv       *Server
	mgr       *session.Manager
	sessionID string

	// pendingWrite is what the stub agent does during the next turn.
	pendingWrite struct {
		path    string
		content string
	}
	files     []changedFileRow
	one       changedFileRow
	lastScope string

	// A held turn writes its file, then waits on holdTurn until the scenario
	// lets it finish; turnWrote says the write is done, turnDone carries the
	// POST's outcome.
	holdTurn  chan struct{}
	turnWrote chan struct{}
	turnDone  chan error

	// events is a subscription to the server-wide event stream.
	events            <-chan []byte
	unsubscribeEvents func()
}

// gherkinText turns the literal backslash-n of a feature file into newlines so
// scenarios can spell out small file bodies inline.
func gherkinText(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

func (s *sessionChangesState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-changes-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.workspace = filepath.Join(root, "workspace")
	s.sessRoot = filepath.Join(root, "sessions")
	s.sessionID = ""
	s.files = nil
	s.one = changedFileRow{}
	s.lastScope = ""
	s.pendingWrite.path = ""
	s.holdTurn, s.turnWrote, s.turnDone = nil, nil, nil
	s.events, s.unsubscribeEvents = nil, nil
	return nil
}

func (s *sessionChangesState) close() {
	// A scenario that failed while a turn was held must not leave the runner
	// blocked for the server shutdown to wait on.
	if s.holdTurn != nil {
		close(s.holdTurn)
		s.holdTurn = nil
	}
	if s.unsubscribeEvents != nil {
		s.unsubscribeEvents()
		s.unsubscribeEvents = nil
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *sessionChangesState) startServer() error {
	for _, d := range []string{s.home, s.workspace, s.sessRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// The stub agent performs whatever edit the scenario queued, standing in
	// for a real tool call so the turn diff is captured the usual way.
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		if s.pendingWrite.path != "" {
			target := filepath.Join(s.workspace, s.pendingWrite.path)
			if err := os.WriteFile(target, []byte(s.pendingWrite.content), 0o644); err != nil {
				return "", err
			}
			s.pendingWrite.path = ""
		}
		// A held turn stays running after its edit, the way a real turn keeps
		// working after one tool call, until the scenario releases it.
		if hold := s.holdTurn; hold != nil {
			close(s.turnWrote)
			<-hold
		}
		// Record the exchange the way the real ReAct loop does: turn diffs are
		// filed under the user-turn count, so a stub that never grows the
		// transcript would overwrite turn 0 on every prompt.
		text := ""
		if len(prompt) > 0 {
			text = prompt[0].Text
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: s.home, CWD: s.workspace},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.workspace, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.workspace)
	s.ts = httptest.NewServer(s.srv.Handler())

	newRes, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	s.sessionID = newRes.SessionID
	return nil
}

func (s *sessionChangesState) workspaceContains(name, content string) error {
	return os.WriteFile(filepath.Join(s.workspace, name), []byte(gherkinText(content)), 0o644)
}

// runTurn sends a prompt and verifies its stored workspace diff.
func (s *sessionChangesState) runTurn(name, content string) error {
	s.pendingWrite.path = name
	s.pendingWrite.content = gherkinText(content)

	before, err := session.ListStoredTurnDiffs(filepath.Join(s.sessRoot, s.sessionID))
	if err != nil {
		return err
	}
	if err := s.postPrompt(); err != nil {
		return err
	}
	return s.awaitStoredTurn(len(before), name)
}

// postPrompt sends one turn and waits for its answer.
func (s *sessionChangesState) postPrompt() error {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses",
		strings.NewReader(`{"model":"agent","input":"edit it","stream":false}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses returned %d", res.StatusCode)
	}
	return nil
}

// awaitStoredTurn waits until more than had turn diffs are on disk.
func (s *sessionChangesState) awaitStoredTurn(had int, name string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		now, err := session.ListStoredTurnDiffs(filepath.Join(s.sessRoot, s.sessionID))
		if err != nil {
			return err
		}
		if len(now) > had {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("turn diff for %q was never stored", name)
}

// startHeldTurn starts a turn that writes one file and then keeps working,
// and returns once the write is done.
func (s *sessionChangesState) startHeldTurn(name, content string) error {
	s.pendingWrite.path = name
	s.pendingWrite.content = gherkinText(content)
	s.holdTurn = make(chan struct{})
	s.turnWrote = make(chan struct{})
	s.turnDone = make(chan error, 1)
	go func() { s.turnDone <- s.postPrompt() }()
	select {
	case <-s.turnWrote:
		return nil
	case err := <-s.turnDone:
		return fmt.Errorf("the turn ended before it wrote: %v", err)
	case <-time.After(10 * time.Second):
		return fmt.Errorf("the turn never wrote %q", name)
	}
}

// finishHeldTurn lets the held turn end and waits for its diff to be stored.
func (s *sessionChangesState) finishHeldTurn() error {
	before, err := session.ListStoredTurnDiffs(filepath.Join(s.sessRoot, s.sessionID))
	if err != nil {
		return err
	}
	close(s.holdTurn)
	s.holdTurn = nil
	select {
	case err := <-s.turnDone:
		if err != nil {
			return err
		}
	case <-time.After(10 * time.Second):
		return fmt.Errorf("the released turn never answered")
	}
	return s.awaitStoredTurn(len(before), "the running turn")
}

// listenForServerEvents subscribes to the stream GET /coddy/events serves.
func (s *sessionChangesState) listenForServerEvents() error {
	s.events, s.unsubscribeEvents = s.srv.events.subscribe()
	return nil
}

// hearChangesRecorded waits for the event that tells the card this session's
// change set can be read.
func (s *sessionChangesState) hearChangesRecorded() error {
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-s.events:
			frame := string(f)
			if strings.HasPrefix(frame, "event: session_changes\n") &&
				strings.Contains(frame, `"sessionId":"`+s.sessionID+`"`) {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("no session_changes event for %s", s.sessionID)
		}
	}
}

func (s *sessionChangesState) askWhatChanged() error {
	return s.askWhatChangedInScope("")
}

// askWhatChangedInScope reads the change set the review window would show for
// one scope; an empty scope exercises the default the card uses.
func (s *sessionChangesState) askWhatChangedInScope(scope string) error {
	s.lastScope = scope
	url := s.ts.URL + "/coddy/sessions/" + s.sessionID + "/changes"
	if scope != "" {
		url += "?scope=" + scope
	}
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET changes returned %d", res.StatusCode)
	}
	var body struct {
		Object string           `json:"object"`
		Files  []changedFileRow `json:"files"`
		Totals struct {
			Files     int `json:"files"`
			Additions int `json:"additions"`
			Deletions int `json:"deletions"`
		} `json:"totals"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if body.Object != "coddy.session_changes" {
		return fmt.Errorf("object = %q", body.Object)
	}
	if body.Totals.Files != len(body.Files) {
		return fmt.Errorf("totals.files = %d but %d files listed", body.Totals.Files, len(body.Files))
	}
	s.files = body.Files
	return nil
}

func (s *sessionChangesState) openDiff(name string) error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sessionID + "/changes/file?path=" + name)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET changes/file returned %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(&s.one)
}

func (s *sessionChangesState) rollBack() error {
	req, err := http.NewRequest(http.MethodPost,
		s.ts.URL+"/coddy/sessions/"+s.sessionID+"/changes/revert", nil)
	if err != nil {
		return err
	}
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST revert returned %d", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

func (s *sessionChangesState) noFilesChanged() error {
	if err := s.askWhatChangedInScope(s.lastScope); err != nil {
		return err
	}
	if len(s.files) != 0 {
		return fmt.Errorf("want no changed files, got %d: %+v", len(s.files), s.files)
	}
	return nil
}

func (s *sessionChangesState) countAndStats(count, additions, deletions int) error {
	if len(s.files) != count {
		return fmt.Errorf("want %d changed file(s), got %d: %+v", count, len(s.files), s.files)
	}
	gotAdd, gotDel := 0, 0
	for _, f := range s.files {
		gotAdd += f.Additions
		gotDel += f.Deletions
	}
	if gotAdd != additions || gotDel != deletions {
		return fmt.Errorf("want +%d -%d, got +%d -%d", additions, deletions, gotAdd, gotDel)
	}
	return nil
}

func (s *sessionChangesState) reportedAs(name, status string) error {
	for _, f := range s.files {
		if f.Path == name {
			if f.Status != status {
				return fmt.Errorf("%s status = %q, want %q", name, f.Status, status)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not in the change set: %+v", name, s.files)
}

func (s *sessionChangesState) diffReplaces(removed, added string) error {
	if !strings.Contains(s.one.Patch, "\n-"+removed+"\n") {
		return fmt.Errorf("patch does not remove %q:\n%s", removed, s.one.Patch)
	}
	if !strings.Contains(s.one.Patch, "\n+"+added+"\n") {
		return fmt.Errorf("patch does not add %q:\n%s", added, s.one.Patch)
	}
	return nil
}

func (s *sessionChangesState) fileContains(name, content string) error {
	got, err := os.ReadFile(filepath.Join(s.workspace, name))
	if err != nil {
		return err
	}
	if string(got) != gherkinText(content) {
		return fmt.Errorf("%s = %q, want %q", name, got, gherkinText(content))
	}
	return nil
}

func (s *sessionChangesState) fileGone(name string) error {
	if _, err := os.Stat(filepath.Join(s.workspace, name)); !os.IsNotExist(err) {
		return fmt.Errorf("%s still exists (%v)", name, err)
	}
	return nil
}

func initializeSessionChangesScenario(sc *godog.ScenarioContext) {
	s := &sessionChangesState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server with a workspace$`, s.startServer)
	sc.Step(`^the workspace contains "([^"]+)" with "([^"]*)"$`, s.workspaceContains)
	sc.Step(`^the agent runs a turn that writes "([^"]+)" as "([^"]*)"$`, s.runTurn)
	sc.Step(`^the agent runs a turn without editing files$`, func() error { return s.runTurn("", "") })
	sc.Step(`^the agent writes "([^"]+)" as "([^"]*)" and keeps working$`, s.startHeldTurn)
	sc.Step(`^the running turn finishes$`, s.finishHeldTurn)
	sc.Step(`^a client listening for server events$`, s.listenForServerEvents)
	sc.Step(`^the client hears that the session's changes are recorded$`, s.hearChangesRecorded)
	sc.Step(`^I ask what the session changed$`, s.askWhatChanged)
	sc.Step(`^I ask what the last turn changed$`, func() error {
		return s.askWhatChangedInScope("turn")
	})
	sc.Step(`^I open the diff for "([^"]+)"$`, s.openDiff)
	sc.Step(`^I roll the session changes back$`, s.rollBack)

	sc.Step(`^no files are reported as changed$`, s.noFilesChanged)
	sc.Step(`^(\d+) files? (?:is|are) reported as changed with (\d+) additions? and (\d+) deletions?$`, s.countAndStats)
	sc.Step(`^"([^"]+)" is reported as "([^"]+)"$`, s.reportedAs)
	sc.Step(`^the diff removes "([^"]+)" and adds "([^"]+)"$`, s.diffReplaces)
	sc.Step(`^"([^"]+)" contains "([^"]*)"$`, s.fileContains)
	sc.Step(`^"([^"]+)" no longer exists$`, s.fileGone)
}

func TestSessionChangesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-changes",
		ScenarioInitializer: initializeSessionChangesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_changes.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session changes feature suite failed")
	}
}
