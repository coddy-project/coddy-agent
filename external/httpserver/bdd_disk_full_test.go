//go:build http

package httpserver

// Godog harness for features/disk_full_http.feature: what a request that needs
// a new session is answered when the volume that holds the sessions folder is full.
// The session manager is real, but creating a session is made to fail the way
// a full volume fails it (a *os.PathError around the platform's code, wrapped
// as the manager wraps it): the manager half of the chain is held by
// features/disk_full.feature in internal/session, and this one holds the
// answer the HTTP surface gives for that error.

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

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type diskFullHTTPState struct {
	root   string
	srv    *Server
	ts     *httptest.Server
	status int
	body   []byte
}

func (s *diskFullHTTPState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-diskfull-bdd-")
	if err != nil {
		return err
	}
	s.root = root
	s.status, s.body = 0, nil
	return nil
}

func (s *diskFullHTTPState) close() {
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

func (s *diskFullHTTPState) startServer() error {
	home := filepath.Join(s.root, "home")
	cwd := filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: "http://127.0.0.1:1", APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b"}},
		Agent:     config.Agent{Model: "local/qwen3-1.7b"},
	}
	log := slog.New(slog.DiscardHandler)
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, nil, log, cwd, store)
	s.srv = New(cfg, mgr, log, cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *diskFullHTTPState) volumeFull() error {
	s.srv.ensureSession = func(context.Context, string, string, string) (*session.State, error) {
		return nil, fmt.Errorf("session/new: layout: %w",
			&os.PathError{Op: "mkdir", Path: filepath.Join(s.root, "sessions", "sess_new"), Err: diskFullErrno})
	}
	return nil
}

func (s *diskFullHTTPState) postFirstMessage(endpoint string) error {
	body := `{"model":"agent","input":"hello"}`
	if endpoint == "/v1/chat/completions" {
		body = `{"model":"agent","messages":[{"role":"user","content":"hello"}]}`
	}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, err = io.ReadAll(res.Body)
	return err
}

// pickFolder is the web UI's first call for a new chat that picked a folder:
// the route creates the session in that folder, before any message is sent.
func (s *diskFullHTTPState) pickFolder() error {
	folder := filepath.Join(s.root, "workspace")
	body := fmt.Sprintf(`{"path":%q}`, folder)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/sessions/"+session.NewSessionID()+"/workspace", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, err = io.ReadAll(res.Body)
	return err
}

func (s *diskFullHTTPState) answerIs507() error {
	if s.status != http.StatusInsufficientStorage {
		return fmt.Errorf("status = %d, want 507; body %s", s.status, s.body)
	}
	return nil
}

func (s *diskFullHTTPState) messageNamesTheCause() error {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(s.body, &env); err != nil {
		return fmt.Errorf("the body is not the error envelope: %v\n%s", err, s.body)
	}
	if !strings.Contains(env.Error.Message, "no space left on device") {
		return fmt.Errorf("error message = %q, want it to say there is no space left on the device", env.Error.Message)
	}
	return nil
}

func initializeDiskFullHTTPScenario(sc *godog.ScenarioContext) {
	s := &diskFullHTTPState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^the volume that holds the sessions folder has no room left$`, s.volumeFull)
	sc.Step(`^a client posts a first message to (\S+) without a session$`, s.postFirstMessage)
	sc.Step(`^a client picks a folder for a new chat$`, s.pickFolder)
	sc.Step(`^the answer is 507 Insufficient Storage$`, s.answerIs507)
	sc.Step(`^the error message says there is no space left on the device$`, s.messageNamesTheCause)
}

func TestDiskFullHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "disk-full-http",
		ScenarioInitializer: initializeDiskFullHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/disk_full_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("disk full HTTP feature failed")
	}
}
