//go:build http

package httpserver

// Godog harness for features/webui_login.feature: the browser sign-in of
// `coddy serve`, driven over the real HTTP surface through the real gate. A
// cookie jar stands in for the browser, the account is a real argon2id hash, and
// a stub runner keeps the whole thing LLM-free.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// loginTestHashParams keep the suite fast; the format is the shipped one.
var loginTestHashParams = webauth.HashParams{Memory: 64, Time: 1, Threads: 1, SaltLen: 8, KeyLen: 16}

type loginFeatureState struct {
	root     string
	sessRoot string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	cfg      *config.Config

	// browser carries the cookie jar, client is the plain one an API client uses.
	browser *http.Client
	client  *http.Client
	bearer  string

	sessionID string

	status  int
	rawBody string
	body    map[string]interface{}
}

func (s *loginFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-login-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sessRoot = filepath.Join(root, "sessions")
	s.bearer = ""
	s.sessionID = ""
	s.status = 0
	s.rawBody = ""
	s.body = nil
	return nil
}

// stopServer tears down the listener and the server, leaving the temp root in
// place so a replacement can boot over it.
func (s *loginFeatureState) stopServer() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
}

func (s *loginFeatureState) close() {
	s.stopServer()
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// start boots a server whose login account comes from config.yaml.
func (s *loginFeatureState) start(user, password string) error {
	login := config.HTTPLoginConfig{}
	if user != "" {
		hash, err := webauth.HashPasswordWith(password, loginTestHashParams)
		if err != nil {
			return err
		}
		login.User, login.PasswordHash = user, hash
	}
	return s.boot(login, LoginCredentials{})
}

// startFromEnv boots a server whose account arrives the way CODDY_HTTP_USER and
// CODDY_HTTP_PASSWORD arrive: out of band, never in the document.
func (s *loginFeatureState) startFromEnv(user, password string) error {
	return s.boot(config.HTTPLoginConfig{}, LoginCredentials{User: user, Password: password})
}

func (s *loginFeatureState) startOpen() error {
	return s.boot(config.HTTPLoginConfig{}, LoginCredentials{})
}

func (s *loginFeatureState) boot(login config.HTTPLoginConfig, env LoginCredentials) error {
	// A scenario that names its own server replaces the one the Background
	// started; without this the first would keep its listener and its manager
	// for the rest of the run.
	s.stopServer()
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessRoot, 0o755); err != nil {
		return err
	}
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		if text := strings.TrimSpace(promptBlocksText(prompt)); text != "" {
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		}
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: s.root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
		HTTPServer: config.HTTPServerConfig{
			Login: login,
		},
	}
	s.cfg = cfg
	store := &session.FileStore{Root: s.sessRoot}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, store)
	s.srv = New(cfg, s.mgr, slog.Default(), s.root)
	if err := s.srv.SetExtraLogin(env.User, env.Password); err != nil {
		return err
	}
	s.ts = httptest.NewServer(s.srv.Handler())

	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	s.browser = &http.Client{Jar: jar}
	s.client = &http.Client{}
	return nil
}

// replaceConfig installs a new configuration the way a save or a file watcher
// does, so the scenarios can prove that a rotated password or a flipped switch
// is felt on the very next request.
func (s *loginFeatureState) replaceConfig(mutate func(*config.Config)) {
	next := *s.cfg
	mutate(&next)
	s.cfg = &next
	s.srv.ReplaceConfig(&next)
}

// --- steps: given ---

func (s *loginFeatureState) serverWithAccount(user, password string) error {
	return s.start(user, password)
}

func (s *loginFeatureState) serverWithEnvAccount(user, password string) error {
	return s.startFromEnv(user, password)
}

func (s *loginFeatureState) serverWithoutCredentials() error { return s.startOpen() }

func (s *loginFeatureState) configTurnsLoginOff() error {
	off := false
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.Enabled = &off })
	return nil
}

func (s *loginFeatureState) serverAlsoRequiresToken(token string) error {
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.AuthToken = token })
	return nil
}

func (s *loginFeatureState) clientPresentsToken(token string) error {
	s.bearer = token
	return nil
}

func (s *loginFeatureState) signedIn(user, password string) error {
	if err := s.signIn(user, password); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return errStatus("sign-in failed", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) operatorChangesPassword(password string) error {
	hash, err := webauth.HashPasswordWith(password, loginTestHashParams)
	if err != nil {
		return err
	}
	s.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.PasswordHash = hash })
	return nil
}

func (s *loginFeatureState) operatorTurnsLoginOff() error { return s.configTurnsLoginOff() }

// --- steps: when ---

// browse issues a request the way the page itself would: cookies attached,
// same-origin labels on, no bearer token anywhere.
func (s *loginFeatureState) browse(method, path string, body interface{}) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", s.ts.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return s.send(s.browser, req)
}

// callAPI issues a request the way `coddy --remote`, an editor or a script
// would: a bearer token, no cookie jar and no browser labels at all.
func (s *loginFeatureState) callAPI(method, path string, body interface{}) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearer)
	}
	return s.send(s.client, req)
}

func (s *loginFeatureState) send(client *http.Client, req *http.Request) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	raw, _ := io.ReadAll(res.Body)
	s.rawBody = string(raw)
	s.body = nil
	var parsed map[string]interface{}
	if json.Unmarshal(raw, &parsed) == nil {
		s.body = parsed
	}
	return nil
}

func (s *loginFeatureState) browserRequestsModels() error {
	return s.browse(http.MethodGet, "/v1/models", nil)
}

func (s *loginFeatureState) browserRequestsSessions() error {
	return s.browse(http.MethodGet, "/coddy/sessions", nil)
}

func (s *loginFeatureState) browserRequestsConfig() error {
	return s.browse(http.MethodGet, "/coddy/config", nil)
}

func (s *loginFeatureState) browserRequestsShell() error {
	return s.browse(http.MethodGet, "/", nil)
}

func (s *loginFeatureState) browserAsksAuthState() error {
	return s.browse(http.MethodGet, "/coddy/auth/me", nil)
}

func (s *loginFeatureState) signIn(user, password string) error {
	return s.browse(http.MethodPost, "/coddy/auth/login", map[string]string{"user": user, "password": password})
}

func (s *loginFeatureState) signOut() error {
	return s.browse(http.MethodPost, "/coddy/auth/logout", nil)
}

// browserOpensEventStream reads the server event stream the way an EventSource
// does - a GET with no token in the query string, which only works because the
// cookie travels on its own.
func (s *loginFeatureState) browserOpensEventStream() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", s.ts.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	u, err := url.Parse(s.ts.URL)
	if err != nil {
		return err
	}
	for _, c := range s.browser.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	res, err := s.browser.Do(req)
	if err != nil {
		return err
	}
	s.status = res.StatusCode
	s.rawBody = ""
	s.body = nil
	// The stream never ends on its own; the status is the whole answer here.
	_ = res.Body.Close()
	return nil
}

// sessionRooted creates a session through the manager, so the scenarios that
// need a state-changing route have something real to change.
func (s *loginFeatureState) sessionRooted() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *loginFeatureState) browserMovesSession() error {
	return s.browse(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/workspace",
		map[string]interface{}{"path": s.root})
}

func (s *loginFeatureState) apiMovesSession() error {
	return s.callAPI(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/workspace",
		map[string]interface{}{"path": s.root})
}

func (s *loginFeatureState) apiRequestsModels() error {
	return s.callAPI(http.MethodGet, "/v1/models", nil)
}

// --- steps: then ---

func (s *loginFeatureState) refused(code int) error {
	if s.status != code {
		return errStatus(fmt.Sprintf("status %d, want %d", s.status, code), s.status, s.rawBody)
	}
	return nil
}

// notRefusedByTheGate is what "public" means for the page itself: whatever the
// shell answers - the SPA in a build that embeds it, a notice in one that does
// not - the answer is not the gate's.
func (s *loginFeatureState) notRefusedByTheGate() error {
	if s.status == http.StatusUnauthorized || s.status == http.StatusForbidden {
		return errStatus("the gate refused a public route", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) succeeds() error {
	if s.status < 200 || s.status >= 300 {
		return errStatus("request failed", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) holdsSessionCookie() error {
	u, err := url.Parse(s.ts.URL)
	if err != nil {
		return err
	}
	for _, c := range s.browser.Jar.Cookies(u) {
		if c.Name == sessionCookieName && c.Value != "" {
			return nil
		}
	}
	return fmt.Errorf("no %q cookie was set", sessionCookieName)
}

func (s *loginFeatureState) answerSaysLoginRequired(required bool) error {
	got, _ := s.body["login_required"].(bool)
	if got != required {
		return errStatus(fmt.Sprintf("login_required = %v, want %v", got, required), s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) answerSaysSignedOut() error {
	if authed, _ := s.body["authenticated"].(bool); authed {
		return errStatus("the answer claims the browser is signed in", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) answerSaysSignedInAs(user string) error {
	if authed, _ := s.body["authenticated"].(bool); !authed {
		return errStatus("the answer does not report a signed-in browser", s.status, s.rawBody)
	}
	if got, _ := s.body["user"].(string); got != user {
		return errStatus("signed in as "+got+", want "+user, s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) modelListIncludes(list string) error {
	data, _ := s.body["data"].([]interface{})
	have := map[string]bool{}
	for _, row := range data {
		if m, ok := row.(map[string]interface{}); ok {
			if id, _ := m["id"].(string); id != "" {
				have[id] = true
			}
		}
	}
	for _, want := range strings.Split(list, ",") {
		if want = strings.TrimSpace(want); want != "" && !have[want] {
			return errStatus("model list is missing "+want, s.status, s.rawBody)
		}
	}
	return nil
}

func (s *loginFeatureState) configHidesPasswordHash() error {
	hs, _ := s.body["httpserver"].(map[string]interface{})
	login, _ := hs["login"].(map[string]interface{})
	if got, _ := login["password_hash"].(string); got != "" {
		return errStatus("the config response returned a password hash", s.status, s.rawBody)
	}
	if hash := s.cfg.HTTPServer.Login.PasswordHash; hash != "" && strings.Contains(s.rawBody, hash) {
		return errStatus("the config response body carries the password hash", s.status, s.rawBody)
	}
	return nil
}

func (s *loginFeatureState) configReportsLoginSource(want string) error {
	hs, _ := s.body["httpserver"].(map[string]interface{})
	if configured, _ := hs["login_configured"].(bool); !configured {
		return errStatus("the config response does not report login_configured", s.status, s.rawBody)
	}
	if got, _ := hs["login_source"].(string); got != want {
		return errStatus("login_source = "+got+", want "+want, s.status, s.rawBody)
	}
	return nil
}

func initializeLoginScenario(sc *godog.ScenarioContext) {
	s := &loginFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy HTTP server with the account "([^"]+)" and password "([^"]+)"$`, s.serverWithAccount)
	sc.Step(`^a coddy HTTP server whose account comes from the environment as "([^"]+)" with password "([^"]+)"$`, s.serverWithEnvAccount)
	sc.Step(`^a coddy HTTP server with no account and no token$`, s.serverWithoutCredentials)
	sc.Step(`^the config turns the login off$`, s.configTurnsLoginOff)
	sc.Step(`^the server also requires the bearer token "([^"]+)"$`, s.serverAlsoRequiresToken)
	sc.Step(`^the browser is signed in as "([^"]+)" with password "([^"]+)"$`, s.signedIn)

	sc.Step(`^the browser requests the model list$`, s.browserRequestsModels)
	sc.Step(`^the browser requests the session list$`, s.browserRequestsSessions)
	sc.Step(`^the browser requests the server config$`, s.browserRequestsConfig)
	sc.Step(`^the browser requests the application shell$`, s.browserRequestsShell)
	sc.Step(`^the browser asks whether sign-in is required$`, s.browserAsksAuthState)
	sc.Step(`^the browser signs in as "([^"]+)" with password "([^"]+)"$`, s.signIn)
	sc.Step(`^the browser signs out$`, s.signOut)
	sc.Step(`^the browser opens the server event stream$`, s.browserOpensEventStream)
	sc.Step(`^a session rooted at the workspace$`, s.sessionRooted)
	sc.Step(`^the browser moves that session from its own origin$`, s.browserMovesSession)
	sc.Step(`^a client presents the bearer token "([^"]+)"$`, s.clientPresentsToken)
	sc.Step(`^the client requests the model list$`, s.apiRequestsModels)
	sc.Step(`^the client moves that session with no browser headers at all$`, s.apiMovesSession)
	sc.Step(`^the operator changes the password to "([^"]+)"$`, s.operatorChangesPassword)
	sc.Step(`^the operator turns the login off$`, s.operatorTurnsLoginOff)

	sc.Step(`^the request is refused with (\d+)$`, s.refused)
	sc.Step(`^the request succeeds$`, s.succeeds)
	sc.Step(`^the request is not refused by the gate$`, s.notRefusedByTheGate)
	sc.Step(`^the browser holds a session cookie$`, s.holdsSessionCookie)
	sc.Step(`^the answer says sign-in is required$`, func() error { return s.answerSaysLoginRequired(true) })
	sc.Step(`^the answer says sign-in is not required$`, func() error { return s.answerSaysLoginRequired(false) })
	sc.Step(`^the answer says the browser is not signed in$`, s.answerSaysSignedOut)
	sc.Step(`^the answer says the browser is signed in as "([^"]+)"$`, s.answerSaysSignedInAs)
	sc.Step(`^the model list includes profiles "([^"]+)"$`, s.modelListIncludes)
	sc.Step(`^the config response hides the password hash$`, s.configHidesPasswordHash)
	sc.Step(`^the config response reports the login source "([^"]+)"$`, s.configReportsLoginSource)
}

func TestWebUILoginFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "webui-login",
		ScenarioInitializer: initializeLoginScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/webui_login.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI login feature suite failed")
	}
}
