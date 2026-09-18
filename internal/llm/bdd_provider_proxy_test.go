package llm

// Godog harness for features/provider_proxy.feature: provider rows reach
// their server the way Coddy reaches it - NewProvider for a completion,
// ListModels, NeuralDeepUsageForProvider, and HTTPClientForProviderProxy for
// a sign-in, as the login commands and routes build it - against three kinds
// of stub servers on loopback. The model server counts the requests that
// reach it directly. The environment's proxy is stood in through the
// environmentProxy seam: net/http reads HTTPS_PROXY once per process and
// never proxies a loopback address, so the real variables cannot stage it.
// The stand-in proxies loopback targets too, so net/http's own rules for the
// real variables - NO_PROXY, the loopback exception - are covered by
// TestProviderProxyFollowsTheProcessEnvironment, which sets them in a child
// process. A proxy of the row's own is the URL its providers[].proxy names.
// A stub proxy answers an absolute-form request itself instead of forwarding
// it, so a request that went through a proxy never reaches the model server.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// proxyStub is a stub server and the number of requests it served.
type proxyStub struct {
	srv   *httptest.Server
	count atomic.Int32
}

type providerProxyRow struct {
	typ     string
	setting string
}

type providerProxyState struct {
	model    *proxyStub
	envProxy *proxyStub
	own      map[string]*proxyStub
	rows     map[string]providerProxyRow
	restore  []func()

	mu       sync.Mutex
	failures []error
}

func (s *providerProxyState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, err)
}

// answerProviderRequest answers every request a provider row makes. The same
// handler serves the model server and, for a proxied request, the proxy that
// stands in for the origin.
func answerProviderRequest(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/chat/completions"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-p1","object":"chat.completion","created":1,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/limits"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, neuralDeepUsageFixture)
	case r.Method == http.MethodPost && path == "/api/cli/device/start":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_code":"dc-1","user_code":"ABCD-EFGH",`+
			`"verification_uri":"https://hub.example/device","interval":5,"expires_in":600}`)
	default:
		return false
	}
	return true
}

// newModelServer counts the requests that reach it as an origin. An
// absolute-form request means a client took it for a proxy, which no row
// ever should.
func (s *providerProxyState) newModelServer() *proxyStub {
	st := &proxyStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() {
			s.fail(fmt.Errorf("the model server got a proxy request for %s", r.URL))
		}
		st.count.Add(1)
		if !answerProviderRequest(w, r) {
			s.fail(fmt.Errorf("the model server has no answer for %s %s", r.Method, r.URL.Path))
			http.NotFound(w, r)
		}
	}))
	return st
}

// newProxyStub counts the requests it carried for the model server.
func (s *providerProxyState) newProxyStub(label string) *proxyStub {
	st := &proxyStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			s.fail(fmt.Errorf("%s was reached as an origin for %s", label, r.URL.Path))
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		if want := strings.TrimPrefix(s.model.srv.URL, "http://"); r.URL.Host != want {
			s.fail(fmt.Errorf("%s carried a request for %s, want the model server %s", label, r.URL.Host, want))
		}
		st.count.Add(1)
		if !answerProviderRequest(w, r) {
			s.fail(fmt.Errorf("%s has no answer for %s %s", label, r.Method, r.URL.Path))
			http.NotFound(w, r)
		}
	}))
	return st
}

func (s *providerProxyState) reset() {
	s.cleanup()
	s.model = s.newModelServer()
	s.own = map[string]*proxyStub{}
	s.rows = map[string]providerProxyRow{}
	s.failures = nil
}

func (s *providerProxyState) cleanup() {
	for _, undo := range slices.Backward(s.restore) {
		undo()
	}
	s.restore = nil
	for _, st := range s.own {
		st.srv.Close()
	}
	s.own = nil
	if s.envProxy != nil {
		s.envProxy.srv.Close()
		s.envProxy = nil
	}
	if s.model != nil {
		s.model.srv.Close()
		s.model = nil
	}
}

// setenv sets a variable for the scenario and puts the previous value back
// after it.
func (s *providerProxyState) setenv(key, value string) {
	prev, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	s.restore = append(s.restore, func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func (s *providerProxyState) theEnvironmentNamesAProxy() error {
	s.envProxy = s.newProxyStub("the environment's proxy")
	u, err := url.Parse(s.envProxy.srv.URL)
	if err != nil {
		return err
	}
	stand := func(*http.Request) (*url.URL, error) { return u, nil }
	prev := environmentProxy.Swap(&stand)
	s.restore = append(s.restore, func() { environmentProxy.Store(prev) })
	return nil
}

func (s *providerProxyState) addRow(typ, name, setting string) error {
	if typ == "neuraldeep" {
		// A neuraldeep row is pinned to the official deployments; the
		// process-wide override is how stands and tests aim it elsewhere.
		s.setenv(EnvNeuralDeepBaseURL, s.model.srv.URL+"/v1")
	}
	s.rows[name] = providerProxyRow{typ: typ, setting: setting}
	return nil
}

func (s *providerProxyState) aProviderWithoutAProxySetting(typ, name string) error {
	return s.addRow(typ, name, "")
}

func (s *providerProxyState) aProviderWithProxy(typ, name, setting string) error {
	return s.addRow(typ, name, setting)
}

func (s *providerProxyState) aProviderWithAProxyOfItsOwn(typ, name string) error {
	st := s.newProxyStub(fmt.Sprintf("the own proxy of %q", name))
	s.own[name] = st
	return s.addRow(typ, name, st.srv.URL)
}

func (s *providerProxyState) row(name string) (providerProxyRow, error) {
	row, ok := s.rows[name]
	if !ok {
		return row, fmt.Errorf("no provider %q in the scenario", name)
	}
	return row, nil
}

func (s *providerProxyState) askCompletion(name string) error {
	row, err := s.row(name)
	if err != nil {
		return err
	}
	p, err := NewProvider(ProviderInput{
		Name:          name,
		Type:          row.typ,
		Model:         "m",
		APIKey:        "k",
		BaseURL:       s.model.srv.URL + "/v1",
		ProxyURL:      row.setting,
		RetryDisabled: true,
	})
	if err != nil {
		s.fail(fmt.Errorf("%s: build the provider: %w", name, err))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "ping"}}, nil)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: completion: %w", name, err))
	case resp.Content != "pong":
		s.fail(fmt.Errorf("%s: completion = %q, want pong", name, resp.Content))
	}
	return nil
}

func (s *providerProxyState) askEverything(name string) error {
	if err := s.askCompletion(name); err != nil {
		return err
	}
	row, err := s.row(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := ListModels(ctx, ProviderInput{
		Name:     name,
		Type:     row.typ,
		APIKey:   "k",
		BaseURL:  s.model.srv.URL + "/v1",
		ProxyURL: row.setting,
	})
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: model list: %w", name, err))
	case len(models) != 1 || models[0].ID != "m":
		s.fail(fmt.Errorf("%s: model list = %+v, want the one model m", name, models))
	}

	usage, err := NeuralDeepUsageForProvider(ctx, config.ProviderConfig{
		Name:   name,
		Type:   row.typ,
		APIKey: "k",
		Proxy:  row.setting,
	}, "")
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: account usage: %w", name, err))
	case usage.Tier != "pro":
		s.fail(fmt.Errorf("%s: account usage tier = %q, want pro", name, usage.Tier))
	}

	// The login command and the settings route build the sign-in client
	// this way from the row they sign in.
	hc, err := HTTPClientForProviderProxy(row.setting)
	if err != nil {
		s.fail(fmt.Errorf("%s: sign-in client: %w", name, err))
		return nil
	}
	login, err := StartNeuralDeepDeviceLogin(ctx, s.model.srv.URL, hc, "bdd")
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: sign-in: %w", name, err))
	case login.UserCode != "ABCD-EFGH":
		s.fail(fmt.Errorf("%s: sign-in code = %q, want ABCD-EFGH", name, login.UserCode))
	}
	return nil
}

func (s *providerProxyState) everyAnswerComesBack() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.failures...)
}

func (s *providerProxyState) theEnvironmentsProxyCarried(n int) error {
	got := 0
	if s.envProxy != nil {
		got = int(s.envProxy.count.Load())
	}
	if got != n {
		return fmt.Errorf("the environment's proxy carried %d requests, want %d", got, n)
	}
	return nil
}

func (s *providerProxyState) theOwnProxyCarried(name string, n int) error {
	got := 0
	if st, ok := s.own[name]; ok {
		got = int(st.count.Load())
	}
	if got != n {
		return fmt.Errorf("the own proxy of %q carried %d requests, want %d", name, got, n)
	}
	return nil
}

func (s *providerProxyState) theModelServerWasReachedDirectly(n int) error {
	if got := int(s.model.count.Load()); got != n {
		return fmt.Errorf("the model server was reached directly %d times, want %d", got, n)
	}
	return nil
}

func initializeProviderProxyScenario(sc *godog.ScenarioContext) {
	s := &providerProxyState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^the environment names a proxy for every request$`, s.theEnvironmentNamesAProxy)
	sc.Step(`^an? "([^"]*)" provider "([^"]*)" without a proxy setting$`, s.aProviderWithoutAProxySetting)
	sc.Step(`^an? "([^"]*)" provider "([^"]*)" with proxy "([^"]*)"$`, s.aProviderWithProxy)
	sc.Step(`^an? "([^"]*)" provider "([^"]*)" with a proxy of its own$`, s.aProviderWithAProxyOfItsOwn)
	sc.Step(`^"([^"]*)" is asked for a completion$`, s.askCompletion)
	sc.Step(`^"([^"]*)" is asked for a completion, its model list, its account usage and a sign-in code$`, s.askEverything)
	sc.Step(`^every answer comes back$`, s.everyAnswerComesBack)
	sc.Step(`^the environment's proxy carried (\d+) requests?$`, s.theEnvironmentsProxyCarried)
	sc.Step(`^the own proxy of "([^"]*)" carried (\d+) requests?$`, s.theOwnProxyCarried)
	sc.Step(`^the model server was reached directly (\d+) times?$`, s.theModelServerWasReachedDirectly)
}

// TestProviderProxyFeature swaps environmentProxy, so it must not run in
// parallel with anything else in the package.
func TestProviderProxyFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "provider-proxy",
		ScenarioInitializer: initializeProviderProxyScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_proxy.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("provider proxy feature suite failed")
	}
}
