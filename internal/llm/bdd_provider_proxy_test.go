package llm

// Godog harness for features/provider_proxy.feature: provider rows reach
// their servers the way Coddy reaches them - NewProvider for a completion or
// a stream, ListModels, the account-usage readers, and the sign-in and
// sign-out helpers, which take the row's providers[].proxy setting as their
// input - against stub servers on loopback. Every class of request a
// provider makes is driven: neuraldeep completions, models, usage and the
// device-auth endpoints; codex streams, catalog, usage, token refresh,
// device sign-in and the login apply; devin JWT mints, catalog, chat, usage
// and the PKCE exchange; openai and anthropic completions and model lists.
// The model server counts the requests that reach it directly. The
// environment's proxy is stood in through the environmentProxy seam: net/http
// reads HTTPS_PROXY once per process and never proxies a loopback address,
// so the real variables cannot stage it. The stand-in proxies loopback
// targets too, so net/http's own rules for the real variables - NO_PROXY,
// the loopback exception - are covered by
// TestProviderProxyFollowsTheProcessEnvironment, which sets them in a child
// process. A proxy of the row's own is the URL its providers[].proxy names.
// A stub proxy answers an absolute-form request itself instead of forwarding
// it, so a request that went through a proxy never reaches the origin.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
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
	t         *testing.T
	model     *proxyStub
	devin     *proxyStub
	devinFake *devinfake.Server
	envProxy  *proxyStub
	own       map[string]*proxyStub
	rows      map[string]providerProxyRow
	origins   map[string]bool
	home      string
	restore   []func()

	mu       sync.Mutex
	failures []error
}

func (s *providerProxyState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, err)
}

// answerProviderRequest answers every request a provider row makes. The same
// handler serves the origin server and, for a proxied request, the proxy
// that stands in for the origin.
func answerProviderRequest(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/chat/completions"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-p1","object":"chat.completion","created":1,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/v1/messages"):
		// Anthropic Messages API.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m",`+
			`"content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":1,"output_tokens":1}}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/models") && r.URL.Query().Get("client_version") != "":
		// The Codex catalog.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"slug":"m","display_name":"Model m","visibility":"list","priority":1,"context_window":272000}]}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/limits"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, neuralDeepUsageFixture)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/wham/usage"):
		// The Codex subscription usage endpoint.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"plan_type":"pro"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/responses"):
		// The Codex Responses stream.
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "pong"})
		sseCompleted(w)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/oauth/token"):
		// Codex token refresh and the device-code exchange share this shape.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id_token":"`+makeJWT(time.Now().Add(time.Hour))+`",`+
			`"access_token":"`+makeJWT(time.Now().Add(time.Hour))+`","refresh_token":"rt-new"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/api/accounts/deviceauth/usercode"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_auth_id":"da-1","user_code":"ABCD-EFGH","interval":"0"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/api/accounts/deviceauth/token"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authorization_code":"ac-1","code_challenge":"cc-1","code_verifier":"cv-1"}`)
	case r.Method == http.MethodPost && path == "/api/cli/device/start":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"device_code":"dc-1","user_code":"ABCD-EFGH",`+
			`"verification_uri":"https://hub.example/device","interval":5,"expires_in":600}`)
	case r.Method == http.MethodPost && path == "/api/cli/device/token":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"nd-key"}`)
	case r.Method == http.MethodGet && path == "/api/cli/whoami":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"email":"dev@example.com","name":"Stand User","tier":"pro"}`)
	case r.Method == http.MethodGet && path == "/api/cli/status":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tier":"pro","models":[{"id":"m","ctx":131072}]}`)
	case r.Method == http.MethodPost && path == "/api/cli/revoke":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
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

// newDevinServer mounts a devinfake stand behind the same counting rules as
// the model server. Devin's Connect endpoints and its sign-in pages are
// served by the stand; the seat-management usage RPC it does not know is
// answered here directly.
func (s *providerProxyState) newDevinServer() *proxyStub {
	fake := devinfake.New(devinfake.Options{
		Models: []devinfake.Model{{Label: "m", UID: "m", ContextWindow: 200000, MaxOutput: 4096}},
	})
	s.devinFake = fake
	st := &proxyStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.IsAbs() {
			s.fail(fmt.Errorf("the devin server got a proxy request for %s", r.URL))
		}
		st.count.Add(1)
		s.answerDevinRequest(w, r)
	}))
	return st
}

// newProxyStub counts the requests it carried for any registered origin.
func (s *providerProxyState) newProxyStub(label string) *proxyStub {
	st := &proxyStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			s.fail(fmt.Errorf("%s was reached as an origin for %s", label, r.URL.Path))
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		if !s.origins[r.URL.Host] {
			s.fail(fmt.Errorf("%s carried a request for %s, which is not a provider's server", label, r.URL.Host))
		}
		st.count.Add(1)
		if s.devin != nil && r.URL.Host == strings.TrimPrefix(s.devin.srv.URL, "http://") {
			s.answerDevinRequest(w, r)
			return
		}
		if !answerProviderRequest(w, r) {
			s.fail(fmt.Errorf("%s has no answer for %s %s", label, r.Method, r.URL.Path))
			http.NotFound(w, r)
		}
	}))
	return st
}

func (s *providerProxyState) answerDevinRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == devinUsageGetUserStatus {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(devinUsageResponse(devinUsagePlan("pro", DevinUsageQuotaBillingStrategy), nil))
		return
	}
	s.devinFake.ServeHTTP(w, r)
}

func (s *providerProxyState) reset(t *testing.T) {
	s.cleanup()
	s.t = t
	s.model = s.newModelServer()
	s.own = map[string]*proxyStub{}
	s.rows = map[string]providerProxyRow{}
	s.origins = map[string]bool{strings.TrimPrefix(s.model.srv.URL, "http://"): true}
	s.failures = nil
	s.home = t.TempDir()
	resetDevinCaches()
}

// resetDevinCaches drops the process-wide JWT and catalog caches so request
// counts are deterministic within and across scenarios.
func resetDevinCaches() {
	devinJWTCache.mu.Lock()
	devinJWTCache.m = map[string]*devinJWTEntry{}
	devinJWTCache.mu.Unlock()
	devinCatalogCache.mu.Lock()
	devinCatalogCache.m = map[string]*devinCatalogCacheEntry{}
	devinCatalogCache.mu.Unlock()
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
	if s.devin != nil {
		s.devin.srv.Close()
		s.devin = nil
		s.devinFake = nil
	}
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
	switch typ {
	case "neuraldeep":
		// A neuraldeep row is pinned to the official deployments; the
		// process-wide override is how stands and tests aim it elsewhere.
		s.setenv(EnvNeuralDeepBaseURL, s.model.srv.URL+"/v1")
		s.setenv(EnvNeuralDeepHubURL, s.model.srv.URL)
	case "codex":
		s.setenv(EnvCodexBaseURL, s.model.srv.URL)
	case "devin":
		if s.devin == nil {
			s.devin = s.newDevinServer()
			s.origins[strings.TrimPrefix(s.devin.srv.URL, "http://")] = true
		}
		s.setenv(EnvDevinAPIServerURL, s.devin.srv.URL)
		s.setenv(EnvDevinWebappURL, s.devin.srv.URL)
		s.setenv(EnvDevinAPIURL, s.devin.srv.URL)
		s.setenv(EnvDevinCLICredentials, filepath.Join(s.home, "absent.toml"))
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

// writeCodexCredential writes a Codex auth file with a still-valid access
// token, so reads do not trigger a token refresh.
func (s *providerProxyState) writeCodexCredential(expired bool) string {
	exp := time.Now().Add(time.Hour)
	if expired {
		exp = time.Now().Add(-time.Hour)
	}
	auth := codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken:  makeJWT(exp),
			RefreshToken: "rt-1",
			AccountID:    "acct-1",
		},
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		s.fail(fmt.Errorf("codex credential encode: %w", err))
		return ""
	}
	path := filepath.Join(s.home, fmt.Sprintf("codex-auth-%d.json", len(s.rows)))
	if err := SaveCodexAuthFile(path, data); err != nil {
		s.fail(fmt.Errorf("codex credential write: %w", err))
		return ""
	}
	return path
}

func (s *providerProxyState) testConfig() *config.Config {
	return &config.Config{Paths: config.Paths{Home: s.home, ConfigPath: filepath.Join(s.home, "config.yaml")}}
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

func (s *providerProxyState) askCompletionAndModels(name string) error {
	if err := s.askCompletion(name); err != nil {
		return err
	}
	row, err := s.row(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base := s.model.srv.URL + "/v1"
	if row.typ == "anthropic" {
		base = s.model.srv.URL
	}
	models, err := ListModels(ctx, ProviderInput{
		Name:     name,
		Type:     row.typ,
		APIKey:   "k",
		BaseURL:  base,
		ProxyURL: row.setting,
	})
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: model list: %w", name, err))
	case len(models) != 1 || models[0].ID != "m":
		s.fail(fmt.Errorf("%s: model list = %+v, want the one model m", name, models))
	}
	return nil
}

func (s *providerProxyState) askNeuralDeepEverything(name string) error {
	row, err := s.row(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.askCompletion(name); err != nil {
		return err
	}
	if err := s.askCompletionAndModels(name); err != nil {
		return err
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

	hub := s.model.srv.URL
	authPath := filepath.Join(s.home, "neuraldeep-auth.json")
	key, err := NeuralDeepDeviceSignIn(ctx, hub, row.setting, authPath, "bdd", nil)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: device sign-in: %w", name, err))
	case key != "nd-key":
		s.fail(fmt.Errorf("%s: device sign-in key = %q, want nd-key", name, key))
	}
	who, err := FetchNeuralDeepWhoami(ctx, hub, key, row.setting)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: whoami: %w", name, err))
	case who.Tier != "pro":
		s.fail(fmt.Errorf("%s: whoami tier = %q, want pro", name, who.Tier))
	}
	st, err := FetchNeuralDeepStatus(ctx, hub, key, row.setting)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: status: %w", name, err))
	case len(st.Models) != 1 || st.Models[0].ID != "m":
		s.fail(fmt.Errorf("%s: status models = %+v, want m", name, st.Models))
	}
	if _, err := ApplyNeuralDeepLoginToConfig(ctx, s.testConfig(), name, hub, "", key, row.setting); err != nil {
		s.fail(fmt.Errorf("%s: apply the login: %w", name, err))
	}
	if err := RevokeNeuralDeepKey(ctx, hub, key, row.setting); err != nil {
		s.fail(fmt.Errorf("%s: sign-out: %w", name, err))
	}
	return nil
}

func (s *providerProxyState) askCodexEverything(name string) error {
	row, err := s.row(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	authPath := s.writeCodexCredential(false)

	// The Responses stream.
	p, err := NewProvider(ProviderInput{
		Name:     name,
		Type:     row.typ,
		Model:    "m",
		AuthPath: authPath,
		ProxyURL: row.setting,
	})
	if err != nil {
		s.fail(fmt.Errorf("%s: build the provider: %w", name, err))
		return nil
	}
	resp, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "ping"}}, nil)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: completion: %w", name, err))
	case !strings.Contains(resp.Content, "pong"):
		s.fail(fmt.Errorf("%s: completion = %q, want pong", name, resp.Content))
	}

	// The model catalog.
	models, err := ListModels(ctx, ProviderInput{
		Name:     name,
		Type:     row.typ,
		AuthPath: authPath,
		ProxyURL: row.setting,
	})
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: model list: %w", name, err))
	case len(models) != 1 || models[0].ID != "m":
		s.fail(fmt.Errorf("%s: model list = %+v, want m", name, models))
	}

	// The subscription usage.
	usage, err := CodexUsageForProvider(ctx, config.ProviderConfig{
		Name:  name,
		Type:  row.typ,
		Proxy: row.setting,
	}, authPath, false)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: account usage: %w", name, err))
	case usage.PlanType != "pro":
		s.fail(fmt.Errorf("%s: account usage plan = %q, want pro", name, usage.PlanType))
	}

	// The OAuth refresh the auth source performs on an expired access token.
	hc, err := HTTPClientForProviderProxy(row.setting)
	if err != nil {
		s.fail(fmt.Errorf("%s: sign-in client: %w", name, err))
		return nil
	}
	expiredPath := s.writeCodexCredential(true)
	src := newManagedCodexAuthSource(expiredPath, false, hc)
	src.tokenURL = s.model.srv.URL + "/oauth/token"
	if _, err := src.Credential(ctx); err != nil {
		s.fail(fmt.Errorf("%s: token refresh: %w", name, err))
	}

	// The device sign-in: usercode, device poll and the token exchange.
	signInPath := filepath.Join(s.home, "codex-device-auth.json")
	if err := CodexDeviceSignIn(ctx, s.model.srv.URL, row.setting, signInPath, nil); err != nil {
		s.fail(fmt.Errorf("%s: device sign-in: %w", name, err))
	}

	// The login apply fetches the catalog one more time.
	if _, err := ApplyCodexLoginToConfig(ctx, s.testConfig(), name, authPath, row.setting); err != nil {
		s.fail(fmt.Errorf("%s: apply the login: %w", name, err))
	}
	return nil
}

func (s *providerProxyState) askDevinEverything(name string) error {
	row, err := s.row(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token := "devin-session-token$stand-token"

	if _, _, err := VerifyDevinCredential(ctx, row.setting, token, "", false); err != nil {
		s.fail(fmt.Errorf("%s: credential check: %w", name, err))
	}
	models, err := ListModels(ctx, ProviderInput{
		Name:     name,
		Type:     row.typ,
		APIKey:   token,
		ProxyURL: row.setting,
	})
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: model list: %w", name, err))
	case len(models) != 1 || models[0].ID != "m":
		s.fail(fmt.Errorf("%s: model list = %+v, want m", name, models))
	}
	p, err := NewProvider(ProviderInput{
		Name:     name,
		Type:     row.typ,
		Model:    "m",
		APIKey:   token,
		ProxyURL: row.setting,
	})
	if err != nil {
		s.fail(fmt.Errorf("%s: build the provider: %w", name, err))
		return nil
	}
	resp, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "ping"}}, nil)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: chat answer: %w", name, err))
	case !strings.Contains(resp.Content, "ok"):
		s.fail(fmt.Errorf("%s: chat answer = %q, want ok", name, resp.Content))
	}
	usage, err := DevinUsageForProvider(ctx, config.ProviderConfig{
		Name:   name,
		Type:   row.typ,
		APIKey: token,
		Proxy:  row.setting,
	}, "", false)
	switch {
	case err != nil:
		s.fail(fmt.Errorf("%s: account usage: %w", name, err))
	case usage.PlanName != "pro":
		s.fail(fmt.Errorf("%s: account usage plan = %q, want pro", name, usage.PlanName))
	}

	// A fresh sign-in mints a JWT for the exchanged credential; the caches are
	// cold so that mint is part of what the sign-in asks of the server.
	resetDevinCaches()
	pr, pw := io.Pipe()
	prompts := make(chan DevinLoginPrompt, 1)
	done := make(chan error, 1)
	authPath := filepath.Join(s.home, "devin-auth.json")
	go func() {
		_, err := DevinSignIn(ctx, row.setting, authPath, DevinSignInOptions{
			Paste:    pr,
			OnPrompt: func(p DevinLoginPrompt) { prompts <- p },
		})
		done <- err
	}()
	var prompt DevinLoginPrompt
	select {
	case prompt = <-prompts:
	case err := <-done:
		s.fail(fmt.Errorf("%s: sign-in before the prompt: %w", name, err))
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	// The browser leg of the sign-in also follows the row's route in this
	// scenario: the fetch registers the PKCE challenge at the stand and comes
	// back with the code the sign-in then exchanges.
	hc, err := HTTPClientForProviderProxy(row.setting)
	if err != nil {
		s.fail(fmt.Errorf("%s: sign-in client: %w", name, err))
		return nil
	}
	browser := *hc
	browser.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp2, err := browser.Get(prompt.AuthURL)
	if err != nil {
		s.fail(fmt.Errorf("%s: fetch the sign-in page: %w", name, err))
		return nil
	}
	location := resp2.Header.Get("Location")
	_ = resp2.Body.Close()
	if location == "" {
		s.fail(fmt.Errorf("%s: the sign-in page did not redirect", name))
		return nil
	}
	if _, err := io.WriteString(pw, location+"\n"); err != nil {
		s.fail(fmt.Errorf("%s: hand the callback to the sign-in: %w", name, err))
		return nil
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		s.fail(fmt.Errorf("%s: sign-in: %w", name, err))
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

func (s *providerProxyState) theProvidersServersWereReachedDirectly(n int) error {
	got := 0
	if s.model != nil {
		got += int(s.model.count.Load())
	}
	if s.devin != nil {
		got += int(s.devin.count.Load())
	}
	if got != n {
		return fmt.Errorf("the provider's servers were reached directly %d times, want %d", got, n)
	}
	return nil
}

func initializeProviderProxyScenario(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		s := &providerProxyState{}
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			s.reset(t)
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
		sc.Step(`^"([^"]*)" is asked for a completion and its model list$`, s.askCompletionAndModels)
		sc.Step(`^"([^"]*)" is asked for a completion, its model list, its account usage and its auth flow$`, s.askNeuralDeepEverything)
		sc.Step(`^"([^"]*)" is asked for a completion, its model list, its account usage, a token refresh, a device sign-in and a config apply$`, s.askCodexEverything)
		sc.Step(`^"([^"]*)" is asked for a credential check, its model list, a chat answer, its account usage and a sign-in$`, s.askDevinEverything)
		sc.Step(`^every answer comes back$`, s.everyAnswerComesBack)
		sc.Step(`^the environment's proxy carried (\d+) requests?$`, s.theEnvironmentsProxyCarried)
		sc.Step(`^the own proxy of "([^"]*)" carried (\d+) requests?$`, s.theOwnProxyCarried)
		sc.Step(`^the provider's servers were reached directly (\d+) times?$`, s.theProvidersServersWereReachedDirectly)
	}
}

// TestProviderProxyFeature swaps environmentProxy, so it must not run in
// parallel with anything else in the package.
func TestProviderProxyFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "provider-proxy",
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_proxy.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	suite.ScenarioInitializer = initializeProviderProxyScenario(t)
	if suite.Run() != 0 {
		t.Fatal("provider proxy feature suite failed")
	}
}
