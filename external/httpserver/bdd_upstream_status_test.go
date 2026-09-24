//go:build http

package httpserver

// Godog harness for features/upstream_error_status.feature: POST
// /v1/chat/completions on a direct model whose REAL openai provider talks to a
// stub that refuses every request with a fixed status, so the status the
// client sees is the one the provider's error carried through Coddy.

import (
	"bytes"
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
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type upstreamStatusState struct {
	root      string
	message   string
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	status    int
	body      string
}

func (s *upstreamStatusState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.backendTS != nil {
		s.backendTS.Close()
		s.backendTS = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *upstreamStatusState) providerAnswers(status int, message string) error {
	root, err := os.MkdirTemp("", "coddy-bdd-upstream-status-*")
	if err != nil {
		return err
	}
	s.root, s.message = root, message
	s.backendTS = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "type": "invalid_request_error", "code": fmt.Sprint(status)}})
	}))
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "workspace")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	model := config.ModelEntry{Model: "local/llama-3.1-8b"}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{model},
		Agent:     config.Agent{Model: model.Model},
	}
	log := slog.Default()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, nil, log, cwd, store)
	s.srv = New(cfg, mgr, log, cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *upstreamStatusState) send(model string, stream bool) error {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, model, stream)
	res, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	s.status, s.body = res.StatusCode, string(raw)
	return nil
}

func (s *upstreamStatusState) posts(model string) error   { return s.send(model, false) }
func (s *upstreamStatusState) streams(model string) error { return s.send(model, true) }

func (s *upstreamStatusState) answerIs(status int) error {
	if s.status != status {
		return fmt.Errorf("HTTP %d, want %d: %s", s.status, status, s.body)
	}
	return nil
}

func (s *upstreamStatusState) checkError(raw string, status int) error {
	e := gjson.Get(raw, "error")
	if got := e.Get("type").String(); got != "upstream_error" {
		return fmt.Errorf("error type = %q, want upstream_error: %s", got, raw)
	}
	if got := e.Get("upstream_status").Int(); got != int64(status) {
		return fmt.Errorf("upstream_status = %d, want %d: %s", got, status, raw)
	}
	if !strings.Contains(e.Get("message").String(), s.message) {
		return fmt.Errorf("error message does not carry the provider's text %q: %s", s.message, raw)
	}
	return nil
}

func (s *upstreamStatusState) blockingError(status int) error {
	return s.checkError(s.body, status)
}

func (s *upstreamStatusState) streamedError(status int) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("stream answered HTTP %d: %s", s.status, s.body)
	}
	for _, f := range parseSSEFrames(s.body) {
		if gjson.Get(f.data, "error").Exists() {
			return s.checkError(f.data, status)
		}
	}
	return fmt.Errorf("the stream carried no error frame: %s", s.body)
}

func initializeUpstreamStatusScenario(sc *godog.ScenarioContext) {
	s := &upstreamStatusState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a coddy server whose direct model's provider answers (\d+) "([^"]*)"$`, s.providerAnswers)
	sc.Step(`^an OpenAI client posts "([^"]+)" without streaming$`, s.posts)
	sc.Step(`^an OpenAI client streams "([^"]+)"$`, s.streams)
	sc.Step(`^the answer is HTTP (\d+)$`, s.answerIs)
	sc.Step(`^the error is an upstream_error with upstream_status (\d+) and the provider's message$`, s.blockingError)
	sc.Step(`^the stream's error frame is an upstream_error with upstream_status (\d+) and the provider's message$`, s.streamedError)
}

func TestUpstreamErrorStatusFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "upstream-error-status",
		ScenarioInitializer: initializeUpstreamStatusScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/upstream_error_status.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("upstream error status feature suite failed")
	}
}
