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

type mtlsState struct {
	t    *testing.T
	fx   *sharedFixture
	ca   *mtlsCA
	base string
	last []*http.Response
	byID map[string][]*http.Response
}

func (s *mtlsState) remote(a, b string, limited bool) error {
	s.byID = map[string][]*http.Response{}
	s.last = nil
	opts := []sharedFixtureOption{withSharedConfig(func(c *config.Config) {
		c.HTTPServer.SharedModels.CertNames = []string{a, b}
	})}
	if limited {
		opts = append(opts, withFakeClock(), withRate(1, 1, 10))
	}
	s.fx, s.ca, s.base = mtlsFixture(s.t, config.SwarmClientAuthOptional, opts...)
	// mtlsFixture lists its own names; the scenario's are the same pair.
	return nil
}

func (s *mtlsState) plain(a, b string) error   { return s.remote(a, b, false) }
func (s *mtlsState) limited(a, b string) error { return s.remote(a, b, true) }

func (s *mtlsState) as(name string) *http.Client {
	cert := s.ca.leaf(s.t, false, name)
	return s.ca.client(&cert)
}

func (s *mtlsState) list(name string) error {
	r := mtlsDo(s.t, s.as(name), http.MethodGet, s.base+llm.CoddyModelsPath, "", nil)
	s.last = append(s.last, r)
	return nil
}

func (s *mtlsState) call(alias, name string, times int) error {
	c := s.as(name)
	for i := 0; i < times; i++ {
		r := mtlsDo(s.t, c, http.MethodPost, s.base+llm.CoddyCompletionsPath, "", wireReq(alias))
		s.last = append(s.last, r)
		s.byID[name] = append(s.byID[name], r)
	}
	return nil
}

func (s *mtlsState) sameCalls(alias string) error { return s.call(alias, "alice.example", 1) }

func (s *mtlsState) readSessions(name string) error {
	r := mtlsDo(s.t, s.as(name), http.MethodGet, s.base+"/coddy/sessions", "", nil)
	s.last = append(s.last, r)
	return nil
}

func (s *mtlsState) bothSucceed() error {
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

func (s *mtlsState) counted(alias, class string) error {
	if _, ok := s.fx.stats().row(alias, class, "ok"); !ok {
		return fmt.Errorf("no ok row for %s in the class %s: %+v", alias, class, s.fx.stats().Rows)
	}
	return nil
}

func (s *mtlsState) answers(code int) error {
	if got := s.last[len(s.last)-1].StatusCode; got != code {
		return fmt.Errorf("the remote answered %d, want %d", got, code)
	}
	return nil
}

func (s *mtlsState) secondRefused(name, code string) error {
	rs := s.byID[name]
	if len(rs) < 2 {
		return fmt.Errorf("%s made %d calls", name, len(rs))
	}
	if rs[0].StatusCode == http.StatusOK {
		newSSEReader(s.t, rs[0]).all()
	}
	e := readError(s.t, rs[1])
	if rs[1].StatusCode != http.StatusTooManyRequests || e.Code != code {
		return fmt.Errorf("the second call: %d %+v", rs[1].StatusCode, e)
	}
	return nil
}

func (s *mtlsState) callSucceeds(name string) error {
	rs := s.byID[name]
	if len(rs) != 1 || rs[0].StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d responses, first %v", name, len(rs), rs)
	}
	newSSEReader(s.t, rs[0]).all()
	return nil
}

func TestSharedModelsMTLSFeature(t *testing.T) {
	st := &mtlsState{t: t}
	suite := godog.TestSuite{
		Name: "shared-models-mtls",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a remote coddy over TLS that admits the certificate names "([^"]*)" and "([^"]*)"$`, st.plain)
			sc.Step(`^a remote coddy over TLS that admits the certificate names "([^"]*)" and "([^"]*)", one call a minute each$`, st.limited)
			sc.Step(`^a client with a certificate for "([^"]*)" lists the shared models$`, st.list)
			sc.Step(`^the same client calls the shared model "([^"]*)"$`, st.sameCalls)
			sc.Step(`^a client with a certificate for "([^"]*)" reads the sessions of the remote$`, st.readSessions)
			sc.Step(`^a client with a certificate for "([^"]*)" calls the shared model "([^"]*)" twice$`, func(n, a string) error { return st.call(a, n, 2) })
			sc.Step(`^a client with a certificate for "([^"]*)" calls the shared model "([^"]*)"$`, func(n, a string) error { return st.call(a, n, 1) })
			sc.Step(`^both requests succeed$`, st.bothSucceed)
			sc.Step(`^the remote counted one call of "([^"]*)" in the class "([^"]*)"$`, st.counted)
			sc.Step(`^the remote answers (\d+)$`, st.answers)
			sc.Step(`^the second call of "([^"]*)" is refused as busy with the code "([^"]*)"$`, st.secondRefused)
			sc.Step(`^the call of "([^"]*)" succeeds$`, st.callSucceeds)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) { return ctx, nil })
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/shared_models_mtls.feature"}, Strict: true, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("shared-models-mtls failed")
	}
}
