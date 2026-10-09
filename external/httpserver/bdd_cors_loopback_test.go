//go:build http

package httpserver

// Godog harness for features/cors_loopback_origins.feature: the laptop case of
// CORS - a page served from the browser's own machine on whatever port its
// coddy serve took - over the server's real HTTP surface. A stub runner, no LLM.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type corsLoopbackState struct {
	ts    *httptest.Server
	srv   *Server
	mgr   *session.Manager
	token string

	status int
	header http.Header
}

func (s *corsLoopbackState) reset() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	s.status, s.header = 0, nil
}

func (s *corsLoopbackState) config(token string, cors config.HTTPCORSConfig) *config.Config {
	return &config.Config{
		Agent:      config.Agent{Model: "openai/gpt-4o"},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		HTTPServer: config.HTTPServerConfig{AuthToken: token, CORS: cors},
	}
}

func (s *corsLoopbackState) start(token string, cors config.HTTPCORSConfig) error {
	s.reset()
	s.token = token
	cfg := s.config(token, cors)
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	log := slog.New(slog.DiscardHandler)
	s.mgr = session.NewManager(cfg, noopSender{}, runner, log, "/tmp", nil)
	s.srv = New(cfg, s.mgr, log, "/tmp")
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *corsLoopbackState) serverAllowingLoopback(token string) error {
	return s.start(token, config.HTTPCORSConfig{Enabled: true, AllowLoopback: true})
}

func (s *corsLoopbackState) serverListingOnly(token, origin string) error {
	return s.start(token, config.HTTPCORSConfig{Enabled: true, AllowedOrigins: []string{origin}})
}

func (s *corsLoopbackState) do(req *http.Request) error {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.header = res.Header
	return nil
}

// preflight is what the browser sends before a request that carries the
// bearer header: OPTIONS with the origin and the method and headers to come,
// and no Authorization of its own.
func (s *corsLoopbackState) preflight(origin, path string) error {
	req, err := http.NewRequest(http.MethodOptions, s.ts.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	return s.do(req)
}

func (s *corsLoopbackState) requestWithToken(origin, path string) error {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Authorization", "Bearer "+s.token)
	return s.do(req)
}

func (s *corsLoopbackState) preflightAnswered(code int) error {
	if s.status != code {
		return fmt.Errorf("preflight status %d, want %d", s.status, code)
	}
	return nil
}

func (s *corsLoopbackState) requestSucceeds() error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status %d, want 200", s.status)
	}
	return nil
}

func (s *corsLoopbackState) allowsOrigin(origin string) error {
	if got := s.header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != origin {
		return fmt.Errorf("Access-Control-Allow-Origin = %q, want exactly [%q]", got, origin)
	}
	return nil
}

func (s *corsLoopbackState) allowsOriginAndVaries(origin string) error {
	if err := s.allowsOrigin(origin); err != nil {
		return err
	}
	if !strings.Contains(strings.Join(s.header.Values("Vary"), ","), "Origin") {
		return fmt.Errorf("an echoed origin needs Vary: Origin, got %q", s.header.Values("Vary"))
	}
	return nil
}

func (s *corsLoopbackState) allowsNoOrigin() error {
	if got := s.header.Get("Access-Control-Allow-Origin"); got != "" {
		return fmt.Errorf("Access-Control-Allow-Origin = %q, want none", got)
	}
	return nil
}

// replaceConfigAllowingLoopback hands the running process a new configuration
// at the one choke point every surface observes - what a PUT /coddy/config
// and an edited file both end in - so the next request reads the new policy.
func (s *corsLoopbackState) replaceConfigAllowingLoopback() error {
	s.mgr.ReplaceConfig(s.config(s.token, config.HTTPCORSConfig{Enabled: true, AllowLoopback: true}))
	return nil
}

func TestCORSLoopbackFeature(t *testing.T) {
	st := &corsLoopbackState{}
	suite := godog.TestSuite{
		Name: "cors-loopback",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a coddy HTTP server with token "([^"]*)" whose CORS allows loopback origins$`, st.serverAllowingLoopback)
			sc.Step(`^a coddy HTTP server with token "([^"]*)" whose CORS lists only "([^"]*)"$`, st.serverListingOnly)
			sc.Step(`^a page at "([^"]*)" sends a preflight for GET "([^"]*)"$`, st.preflight)
			sc.Step(`^a page at "([^"]*)" requests "([^"]*)" with the token$`, st.requestWithToken)
			sc.Step(`^the preflight is answered with (\d+)$`, st.preflightAnswered)
			sc.Step(`^the request succeeds$`, st.requestSucceeds)
			sc.Step(`^the response allows the origin "([^"]*)"$`, st.allowsOrigin)
			sc.Step(`^the response allows the origin "([^"]*)" and varies by origin$`, st.allowsOriginAndVaries)
			sc.Step(`^the response allows no origin$`, st.allowsNoOrigin)
			sc.Step(`^the running server's configuration is replaced with CORS allowing loopback origins$`, st.replaceConfigAllowingLoopback)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				st.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cors_loopback_origins.feature"},
			TestingT: t,
			Output:   os.Stdout,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cors loopback feature failed")
	}
}
