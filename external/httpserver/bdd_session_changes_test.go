//go:build http

package httpserver

// Godog harness for features/session_changes.feature: drives the live
// /coddy/sessions/{id}/changes surface behind the Edits view over a workspace
// that is a git repository, from a turn that edits it through to discarding
// the uncommitted changes.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	files []changedFileRow
	one   changedFileRow
	vcs   string

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
	s.vcs = ""
	s.pendingWrite.path = ""
	s.events, s.unsubscribeEvents = nil, nil
	return nil
}

func (s *sessionChangesState) close() {
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

// git runs the git binary in the workspace for the fixture, with no global or
// system configuration in the way.
func (s *sessionChangesState) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.workspace
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v\n%s", args, err, out)
	}
	return nil
}

// startGitServer is startServer over a workspace that is a fresh git
// repository. A machine without git cannot build it and skips.
func (s *sessionChangesState) startGitServer() error {
	if _, err := exec.LookPath("git"); err != nil {
		return godog.ErrSkip
	}
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return err
	}
	if err := s.git("init", "-b", "main"); err != nil {
		return err
	}
	return s.startServer()
}

func (s *sessionChangesState) startServer() error {
	for _, d := range []string{s.home, s.workspace, s.sessRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// The stub agent performs whatever edit the scenario queued, standing in
	// for a real tool call.
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		if s.pendingWrite.path != "" {
			target := filepath.Join(s.workspace, s.pendingWrite.path)
			if err := os.WriteFile(target, []byte(s.pendingWrite.content), 0o644); err != nil {
				return "", err
			}
			s.pendingWrite.path = ""
		}
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

// committed writes a file and commits it, so HEAD holds it.
func (s *sessionChangesState) committed(name, content string) error {
	if err := os.WriteFile(filepath.Join(s.workspace, name), []byte(gherkinText(content)), 0o644); err != nil {
		return err
	}
	if err := s.git("add", "--", name); err != nil {
		return err
	}
	return s.git("-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "add "+name)
}

// changedOnDisk edits a file the way an editor next to the agent would.
func (s *sessionChangesState) changedOnDisk(name, content string) error {
	return os.WriteFile(filepath.Join(s.workspace, name), []byte(gherkinText(content)), 0o644)
}

// runTurn sends a prompt whose turn writes one file.
func (s *sessionChangesState) runTurn(name, content string) error {
	s.pendingWrite.path = name
	s.pendingWrite.content = gherkinText(content)
	return s.postPrompt()
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

// listenForServerEvents subscribes to the stream GET /coddy/events serves.
func (s *sessionChangesState) listenForServerEvents() error {
	s.events, s.unsubscribeEvents = s.srv.events.subscribe()
	return nil
}

// hearWorkingCopyMoved waits for the event that tells the Edits view to read
// this session's working copy again.
func (s *sessionChangesState) hearWorkingCopyMoved() error {
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

// askWhatChanged reads the change set the Edits view shows.
func (s *sessionChangesState) askWhatChanged() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sessionID + "/changes")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET changes returned %d", res.StatusCode)
	}
	var body struct {
		Object string           `json:"object"`
		VCS    string           `json:"vcs"`
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
	s.vcs = body.VCS
	return nil
}

func (s *sessionChangesState) gitIsNamed() error {
	if s.vcs != "git" {
		return fmt.Errorf("vcs = %q, want git", s.vcs)
	}
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

// discard asks the server to put the working copy back at HEAD: the body
// names the files, or says all of them.
func (s *sessionChangesState) discard(body string) error {
	req, err := http.NewRequest(http.MethodPost,
		s.ts.URL+"/coddy/sessions/"+s.sessionID+"/changes/revert", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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

	sc.Step(`^a running coddy HTTP server whose workspace is a git repository$`, s.startGitServer)
	sc.Step(`^the repository has "([^"]+)" committed as "([^"]*)"$`, s.committed)
	sc.Step(`^"([^"]+)" is changed on disk to "([^"]*)"$`, s.changedOnDisk)
	sc.Step(`^the agent runs a turn that writes "([^"]+)" as "([^"]*)"$`, s.runTurn)
	sc.Step(`^a client listening for server events$`, s.listenForServerEvents)
	sc.Step(`^the client hears that the session's working copy changed$`, s.hearWorkingCopyMoved)
	sc.Step(`^I ask what the working copy changed$`, s.askWhatChanged)
	sc.Step(`^I open the diff for "([^"]+)"$`, s.openDiff)
	sc.Step(`^I discard the changes of "([^"]+)"$`, func(name string) error {
		return s.discard(`{"paths":["` + name + `"]}`)
	})
	sc.Step(`^I discard every change$`, func() error { return s.discard(`{"all":true}`) })

	sc.Step(`^git is named as the version control$`, s.gitIsNamed)
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
