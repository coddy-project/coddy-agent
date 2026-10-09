//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type tlsState struct {
	t       *testing.T
	fx      *sharedFixture
	ca      *mtlsCA
	base    string
	last    []*http.Response
	calls   []*http.Response
	connErr error
}

func (s *tlsState) remote(limited bool) error {
	s.last, s.calls, s.connErr = nil, nil, nil
	var opts []sharedFixtureOption
	if limited {
		opts = append(opts, withFakeClock(), withRate(1, 1, 10))
	}
	s.fx, s.ca, s.base = mtlsFixture(s.t, opts...)
	return nil
}

func (s *tlsState) plain() error   { return s.remote(false) }
func (s *tlsState) limited() error { return s.remote(true) }

func (s *tlsState) as(name string) *http.Client {
	cert := s.ca.leaf(s.t, false, name)
	return s.ca.client(&cert)
}

func (s *tlsState) read(token string) error {
	s.last = append(s.last, mtlsDo(s.t, s.as("alice.example"), http.MethodGet, s.base+llm.CoddyModelsPath, token, nil))
	return nil
}

func (s *tlsState) readNoToken() error { return s.read("") }

func (s *tlsState) list() error { return s.read(sharedTestSharedTok) }

func (s *tlsState) call(alias, name string) error {
	r := mtlsDo(s.t, s.as(name), http.MethodPost, s.base+llm.CoddyCompletionsPath, sharedTestSharedTok, wireReq(alias))
	s.last = append(s.last, r)
	s.calls = append(s.calls, r)
	return nil
}

func (s *tlsState) bothSucceed() error {
	for _, r := range s.last {
		if r.StatusCode != http.StatusOK {
			return fmt.Errorf("a request answered %d", r.StatusCode)
		}
		if r.Header.Get("Content-Type") == "text/event-stream" {
			newSSEReader(s.t, r).all()
		}
	}
	return nil
}

func (s *tlsState) counted(alias, class string) error {
	row, ok := s.fx.stats().row(alias, class, "ok")
	if !ok {
		return fmt.Errorf("no ok row for %s in the class %s: %+v", alias, class, s.fx.stats().Rows)
	}
	if row.Calls != 1 {
		return fmt.Errorf("%d ok calls of %s in the class %s, want 1", row.Calls, alias, class)
	}
	return nil
}

func (s *tlsState) answers(code int) error {
	if got := s.last[len(s.last)-1].StatusCode; got != code {
		return fmt.Errorf("the remote answered %d, want %d", got, code)
	}
	return nil
}

func (s *tlsState) secondRefused(code string) error {
	if len(s.calls) < 2 {
		return fmt.Errorf("%d calls were made", len(s.calls))
	}
	if s.calls[0].StatusCode == http.StatusOK {
		newSSEReader(s.t, s.calls[0]).all()
	}
	e := readError(s.t, s.calls[1])
	if s.calls[1].StatusCode != http.StatusTooManyRequests || e.Code != code {
		return fmt.Errorf("the second call: %d %+v", s.calls[1].StatusCode, e)
	}
	return nil
}

func (s *tlsState) connectWithNoCertificate() error {
	resp, err := s.ca.client(nil).Get(s.base + llm.CoddyModelsPath)
	if err == nil {
		_ = resp.Body.Close()
	}
	s.connErr = err
	return nil
}

func (s *tlsState) handshakeFails() error {
	if s.connErr == nil {
		return fmt.Errorf("a peer without a certificate was served")
	}
	return nil
}

func TestSharedModelsTLSFeature(t *testing.T) {
	st := &tlsState{t: t}
	_ = config.SwarmTLSConfig{}
	suite := godog.TestSuite{
		Name: "shared-models-tls",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a remote coddy over TLS that requires a client certificate$`, st.plain)
			sc.Step(`^a remote coddy over TLS that requires a client certificate, one call a minute$`, st.limited)
			sc.Step(`^a client with a certificate reads the shared models with no token$`, st.readNoToken)
			sc.Step(`^a client with a certificate lists the shared models with the shared token$`, st.list)
			sc.Step(`^the same client calls the shared model "([^"]*)" with the shared token$`, func(a string) error { return st.call(a, "alice.example") })
			sc.Step(`^a client with a first certificate calls the shared model "([^"]*)" with the shared token$`, func(a string) error { return st.call(a, "alice.example") })
			sc.Step(`^a client with a second certificate calls the shared model "([^"]*)" with the shared token$`, func(a string) error { return st.call(a, "bob.example") })
			sc.Step(`^a client with no certificate connects$`, st.connectWithNoCertificate)
			sc.Step(`^both requests succeed$`, st.bothSucceed)
			sc.Step(`^the remote counted one call of "([^"]*)" in the class "([^"]*)"$`, st.counted)
			sc.Step(`^the remote answers (\d+)$`, st.answers)
			sc.Step(`^the second call is refused as busy with the code "([^"]*)"$`, st.secondRefused)
			sc.Step(`^the handshake fails$`, st.handshakeFails)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) { return ctx, nil })
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/shared_models_tls.feature"}, Strict: true, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("shared-models-tls failed")
	}
}
