//go:build http && browser

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const panelKey = "0123456789abcdef0123456789abcdef"

// panelStand is the whole path of a side panel turn: one HTTP server behind
// a token gate serving the web UI and the extension's endpoint, the real
// agent on a scripted model, and the fake extension.
type panelStand struct {
	t       *testing.T
	model   *httptest.Server
	web     *httptest.Server
	ext     *browsertest.Extension
	client  *http.Client
	ticket  string
	webUI   string
	session string
	answer  string
	tabID   int
	// sessions is the root of the session bundles.
	sessions string
}

func (p *panelStand) start() error {
	snapshot, _ := json.Marshal(map[string]any{"action": "snapshot"})
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{
		{Match: "read this page", Tool: &llmstub.ToolCall{Name: "browser", Arguments: snapshot}, Answer: "The page is Example Domain."},
	}}
	p.model = httptest.NewServer(stub.Handler())
	root := p.t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: cwd},
		Providers:  []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: p.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:     []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}},
		Agent:      config.Agent{Model: "stub/coddy-demo"},
		Browser:    config.BrowserConfig{Enable: true, Token: panelKey},
		HTTPServer: config.HTTPServerConfig{AuthToken: "web-ui-token-that-the-panel-never-sees"},
		// The panel's turns run as a person in bypass would: the tool's own
		// permission is not what this scenario is about.
		Tools: config.Tools{PermissionMode: config.PermModeBypass},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	p.sessions = filepath.Join(root, "sessions")
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, &session.FileStore{Root: p.sessions})
	srv := New(cfg, mgr, log, cwd)
	p.t.Cleanup(srv.Drain)
	p.web = httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	p.client = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	return nil
}

func (p *panelStand) connected() error {
	// The extension connects to the HTTP server itself, past its token gate.
	ext, err := browsertest.Connect(p.web.URL, browsertest.Options{Key: panelKey, ID: "browser-panel-bdd-01"})
	if err != nil {
		return err
	}
	p.ext = ext
	ext.Handle = func(c browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]any{"title": "Example Domain", "text": "This domain is for use in examples."}}
	}
	raw, err := ext.Request("panel/open", map[string]any{}, 5*time.Second)
	if err != nil {
		return err
	}
	var opened struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(raw, &opened); err != nil || opened.Ticket == "" {
		return fmt.Errorf("panel/open answered %s", raw)
	}
	// The panel's frame loads the web UI at the address the extension
	// connected to.
	p.webUI, p.ticket = p.web.URL, opened.Ticket
	return nil
}

// do sends a request as the web UI in the frame does: same origin, with the
// cookies the panel's sign-in left.
func (p *panelStand) do(method, path, body string, header map[string]string) (int, string, error) {
	req, err := http.NewRequest(method, p.webUI+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw), nil
}

func (p *panelStand) signsIn() error {
	if code, _, err := p.do(http.MethodGet, "/coddy/sessions", "", nil); err != nil || code != http.StatusUnauthorized {
		return fmt.Errorf("the gate let the panel in before its sign-in: %d %v", code, err)
	}
	code, body, err := p.do(http.MethodPost, "/coddy/auth/browser", fmt.Sprintf(`{"ticket":%q}`, p.ticket), nil)
	if err != nil || code != http.StatusOK || !strings.Contains(body, `"user":"browser:browser-panel-bdd-01"`) {
		return fmt.Errorf("sign-in: %d %s %v", code, body, err)
	}
	code, body, err = p.do(http.MethodGet, "/coddy/auth/me", "", nil)
	if err != nil || !strings.Contains(body, `"authenticated":true`) {
		return fmt.Errorf("me after sign-in: %d %s %v", code, body, err)
	}
	return nil
}

func (p *panelStand) writes(text, url, title string) error {
	p.tabID = 41
	p.session = session.NewSessionID()
	// The panel gives the tab to the chat before the web UI sends the turn.
	if err := p.ext.Publish(browsertest.Tab{ID: p.tabID, URL: url, Title: title, SessionID: p.session}); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for !p.tabKnown() {
		if time.Now().After(deadline) {
			return fmt.Errorf("the tab never reached the hub")
		}
		time.Sleep(5 * time.Millisecond)
	}
	body, _ := json.Marshal(map[string]any{
		"model": "agent", "input": text, "stream": false,
		"metadata": map[string]any{"surface": "webui", "browser": map[string]any{
			"browser_id": "browser-panel-bdd-01", "tab_id": p.tabID, "url": url, "title": title,
		}},
	})
	code, resp, err := p.do(http.MethodPost, "/v1/responses", string(body), map[string]string{"X-Coddy-Session-ID": p.session})
	if err != nil || code != http.StatusOK {
		return fmt.Errorf("the turn: %d %s %v", code, resp, err)
	}
	p.answer = resp
	return nil
}

func (p *panelStand) tabKnown() bool {
	for _, b := range browser.Default().List(p.session) {
		for _, t := range b.Tabs {
			if t.ID == p.tabID && t.Yours {
				return true
			}
		}
	}
	return false
}

func (p *panelStand) receives(action string) error {
	cmd, err := p.ext.Next(10 * time.Second)
	if err != nil {
		return err
	}
	if cmd.Action != action || cmd.TabID != p.tabID || cmd.Session.ID != p.session {
		return fmt.Errorf("the extension received %+v", cmd)
	}
	return nil
}

func (p *panelStand) answered(text string) error {
	if !strings.Contains(p.answer, text) {
		return fmt.Errorf("the web UI received %s", p.answer)
	}
	return nil
}

// keptOnlyForTheTurn checks the bundle once the turn is over: the browser
// stays selected for the chat, the tab was its default for that turn alone.
func (p *panelStand) keptOnlyForTheTurn() error {
	dir := filepath.Join(p.sessions, p.session)
	raw, err := os.ReadFile(filepath.Join(dir, browser.SelectionFile))
	if err != nil || string(raw) != `"browser-panel-bdd-01"` {
		return fmt.Errorf("the selected browser: %q %v", raw, err)
	}
	if tab := browser.PanelTabOf(dir, "browser-panel-bdd-01"); tab != 0 {
		return fmt.Errorf("tab %d is still the chat's default after its turn", tab)
	}
	return nil
}

func (p *panelStand) close() {
	if p.ext != nil {
		_ = p.ext.Close()
	}
	for _, s := range []*httptest.Server{p.web, p.model} {
		if s != nil {
			s.Close()
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && browser.Default().Connected("browser-panel-bdd-01") {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBrowserPanelFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "browser-panel",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			p := &panelStand{t: t}
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				p.close()
				return ctx, err
			})
			sc.Step(`^a Coddy with a token-gated HTTP server, the browser enabled and a scripted model$`, p.start)
			sc.Step(`^Coddy for Browser connected and its side panel opened$`, p.connected)
			sc.Step(`^the panel's web UI signs in with the ticket of the extension's endpoint$`, p.signsIn)
			sc.Step(`^the user writes "([^"]*)" in the panel from the tab "([^"]*)" titled "([^"]*)"$`, p.writes)
			sc.Step(`^the extension receives a "([^"]*)" command for that tab from the panel's chat$`, p.receives)
			sc.Step(`^the web UI receives the answer "([^"]*)"$`, p.answered)
			sc.Step(`^the chat keeps the panel's browser, and the tab only for that turn$`, p.keptOnlyForTheTurn)
		},
		Options: &godog.Options{Format: "progress", Paths: []string{"../../features/browser_panel.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("browser panel scenarios failed")
	}
}
