//go:build http

package httpserver

// Godog harness for features/loopback_request_guard.feature: what a server
// without a credential answers on loopback, over its real HTTP surface. A stub
// runner, no LLM.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type loopbackGuardState struct {
	t      *testing.T
	ts     *httptest.Server
	status int
}

func (s *loopbackGuardState) serverWithoutCredential() error {
	s.ts = newLoopbackGuardServer(s.t, config.HTTPServerConfig{})
	return nil
}

func (s *loopbackGuardState) serverWithToken(token string) error {
	s.ts = newLoopbackGuardServer(s.t, config.HTTPServerConfig{AuthToken: token})
	return nil
}

func (s *loopbackGuardState) port() string {
	u := strings.TrimPrefix(s.ts.URL, "http://")
	return u[strings.LastIndex(u, ":"):]
}

func (s *loopbackGuardState) postWithoutOrigin(path string) error {
	s.status = loopbackGuardDo(s.t, s.ts, http.MethodPost, path, "", "")
	return nil
}

func (s *loopbackGuardState) postFromOwnOrigin(path string) error {
	s.status = loopbackGuardDo(s.t, s.ts, http.MethodPost, path, "", s.ts.URL)
	return nil
}

func (s *loopbackGuardState) postFromPage(origin, path string) error {
	s.status = loopbackGuardDo(s.t, s.ts, http.MethodPost, path, "", origin)
	return nil
}

func (s *loopbackGuardState) getByName(host, path string) error {
	s.status = loopbackGuardDo(s.t, s.ts, http.MethodGet, path, host+s.port(), "")
	return nil
}

func (s *loopbackGuardState) passes() error {
	if s.status == http.StatusMisdirectedRequest || s.status == http.StatusForbidden {
		return fmt.Errorf("status %d, want the request to reach the route", s.status)
	}
	return nil
}

func (s *loopbackGuardState) refusedWith(code int) error {
	if s.status != code {
		return fmt.Errorf("status %d, want %d", s.status, code)
	}
	return nil
}

func TestLoopbackGuardFeature(t *testing.T) {
	st := &loopbackGuardState{t: t}
	suite := godog.TestSuite{
		Name: "loopback-guard",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a coddy HTTP server on loopback without a credential$`, st.serverWithoutCredential)
			sc.Step(`^a coddy HTTP server on loopback with token "([^"]*)"$`, st.serverWithToken)
			sc.Step(`^a client without an origin posts to "([^"]*)"$`, st.postWithoutOrigin)
			sc.Step(`^a page of the server's own origin posts to "([^"]*)"$`, st.postFromOwnOrigin)
			sc.Step(`^a page at "([^"]*)" posts to "([^"]*)"$`, st.postFromPage)
			sc.Step(`^a client asks "([^"]*)" for "([^"]*)"$`, st.getByName)
			sc.Step(`^the request passes the guard$`, st.passes)
			sc.Step(`^the request is refused with (\d+)$`, st.refusedWith)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if st.ts != nil {
					st.ts.Close()
					st.ts = nil
				}
				st.status = 0
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/loopback_request_guard.feature"},
			TestingT: t,
			Output:   os.Stdout,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("loopback guard feature failed")
	}
}
