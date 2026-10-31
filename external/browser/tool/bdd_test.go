//go:build browser

package browsertool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const testKey = "0123456789abcdef0123456789abcdef"

// browserWorld is one scenario: a real listener on httptest, the fake
// extension dialling it over a real WebSocket, and a chat calling the real
// tool.
type browserWorld struct {
	t         *testing.T
	srv       *httptest.Server
	ext       *browsertest.Extension
	env       *tooling.Env
	tabID     int
	pending   chan toolOutcome
	lastOut   string
	lastCmd   browsertest.Command
	openedTab int
	// cfg is the configuration of the process the chat runs in: nil for the
	// home server itself, browser.remotes for a chat of another process.
	cfg *config.Config
	// browserName is the browser as the chat names it.
	browserName string
}

type toolOutcome struct {
	out string
	err error
}

func (w *browserWorld) connected() error {
	w.srv = httptest.NewServer(browser.NewServer(browser.Default(), func() string { return testKey }, nil))
	ext, err := browsertest.Connect(w.srv.URL, browsertest.Options{Key: testKey, ID: "browser-bdd-0001"})
	if err != nil {
		return err
	}
	w.ext = ext
	// The extension announces what is shared right after initialize, an
	// empty list included, under the grant initialize carried.
	if err := ext.Publish(); err != nil {
		return err
	}
	w.env = &tooling.Env{SessionID: "sess_bdd_chat", SessionDir: w.t.TempDir(), CWD: w.t.TempDir()}
	return waitFor(func() bool { return browser.Default().Connected("browser-bdd-0001") })
}

// connectedHome is the home server of the cross-process scenario: the
// extension's endpoint and the API other processes call, on one mux, as the
// HTTP server mounts them.
func (w *browserWorld) connectedHome() error {
	mux := http.NewServeMux()
	mux.Handle("/browser/", browser.NewServer(browser.Default(), func() string { return testKey }, nil))
	(&browser.API{Hub: browser.Default()}).Register(mux)
	w.srv = httptest.NewServer(mux)
	ext, err := browsertest.Connect(w.srv.URL, browsertest.Options{Key: testKey, ID: "browser-bdd-0001"})
	if err != nil {
		return err
	}
	w.ext = ext
	if err := ext.Publish(); err != nil {
		return err
	}
	w.env = &tooling.Env{SessionID: "sess_bdd_chat", SessionDir: w.t.TempDir(), CWD: w.t.TempDir()}
	return waitFor(func() bool { return browser.Default().Connected("browser-bdd-0001") })
}

// remoteListed lists the browsers from a process whose configuration names
// the home server in browser.remotes.
func (w *browserWorld) remoteListed(node string) error {
	w.cfg = &config.Config{
		Browser: config.BrowserConfig{Enable: true, Remotes: []string{w.srv.URL}},
		Swarm:   config.SwarmConfig{Join: []config.SwarmJoin{{Name: node}}},
	}
	return w.listed()
}

func (w *browserWorld) seesTabOnServer(title string) error {
	if err := w.seesTab(title); err != nil {
		return err
	}
	w.browserName = "browser-bdd-0001@" + strings.TrimPrefix(w.srv.URL, "http://")
	if !strings.Contains(w.lastOut, `"id":"`+w.browserName+`"`) {
		return fmt.Errorf("the list does not name the browser %q: %s", w.browserName, w.lastOut)
	}
	return nil
}

func (w *browserWorld) snapshotIn() error {
	w.ext.Handle = func(c browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]any{"title": "Example Domain", "text": "This domain is for use in examples."}}
	}
	w.call(map[string]any{"action": "snapshot", "tab_id": w.tabID, "browser_id": w.browserName})
	return nil
}

func (w *browserWorld) receivedFromNode(action, node string) error {
	if err := w.received(action); err != nil {
		return err
	}
	if w.lastCmd.TabID != w.tabID {
		return fmt.Errorf("the command went to tab %d", w.lastCmd.TabID)
	}
	if want := node + "/" + w.env.SessionID; w.lastCmd.Session.ID != want || w.lastCmd.Session.Node != node {
		return fmt.Errorf("the command came from %q on %q, not %q on %q", w.lastCmd.Session.ID, w.lastCmd.Session.Node, want, node)
	}
	return nil
}

func (w *browserWorld) shared(url, title string) error {
	w.tabID = 41
	if err := w.ext.Share(browsertest.Tab{ID: w.tabID, URL: url, Title: title}); err != nil {
		return err
	}
	return waitFor(func() bool {
		for _, b := range browser.Default().List("") {
			if len(b.Tabs) == 1 {
				return true
			}
		}
		return false
	})
}

func (w *browserWorld) call(args map[string]any) {
	raw, _ := json.Marshal(args)
	w.pending = make(chan toolOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := execute(ctx, w.cfg, string(raw), w.env)
		w.pending <- toolOutcome{out, err}
	}()
}

func (w *browserWorld) outcome() (string, error) {
	select {
	case o := <-w.pending:
		return o.out, o.err
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("the tool call did not finish")
	}
}

func (w *browserWorld) listed() error {
	w.call(map[string]any{"action": "list"})
	out, err := w.outcome()
	w.lastOut = out
	return err
}

func (w *browserWorld) seesTab(title string) error {
	if !strings.Contains(w.lastOut, `"title":"`+title+`"`) {
		return fmt.Errorf("the list does not show %q: %s", title, w.lastOut)
	}
	return nil
}

func (w *browserWorld) snapshot() error {
	w.ext.Handle = func(c browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]any{"title": "Example Domain", "text": "This domain is for use in examples."}}
	}
	w.call(map[string]any{"action": "snapshot", "tab_id": w.tabID})
	return nil
}

func (w *browserWorld) received(action string) error {
	cmd, err := w.ext.Next(5 * time.Second)
	if err != nil {
		return err
	}
	w.lastCmd = cmd
	if cmd.Action != action {
		return fmt.Errorf("the extension received %q, not %q", cmd.Action, action)
	}
	if cmd.Grant != w.ext.Grant() {
		return fmt.Errorf("the command carries grant %q, the extension's is %q", cmd.Grant, w.ext.Grant())
	}
	return nil
}

func (w *browserWorld) receivedForTab(action string) error {
	if err := w.received(action); err != nil {
		return err
	}
	if w.lastCmd.TabID != w.tabID || w.lastCmd.Session.ID != w.env.SessionID {
		return fmt.Errorf("the command went to tab %d for %q", w.lastCmd.TabID, w.lastCmd.Session.ID)
	}
	return nil
}

func (w *browserWorld) pageText(text string) error {
	out, err := w.outcome()
	if err != nil {
		return err
	}
	if !strings.Contains(out, text) || !strings.Contains(out, "untrusted") {
		return fmt.Errorf("the chat got %q", out)
	}
	return nil
}

func (w *browserWorld) opensTab(url string) error {
	w.openedTab = 77
	w.ext.Handle = func(c browsertest.Command) browsertest.Answer {
		if c.Action != "open_tab" {
			return browsertest.Answer{Result: map[string]bool{"ok": true}}
		}
		// The real extension announces the new tab, in the chat's group,
		// before it answers.
		if err := w.ext.Publish(browsertest.Tab{ID: w.openedTab, URL: url, Title: "Example", SessionID: c.Session.ID}); err != nil {
			return browsertest.Answer{Error: err.Error()}
		}
		return browsertest.Answer{Result: map[string]any{"tabId": w.openedTab, "url": url}}
	}
	w.call(map[string]any{"action": "open_tab", "params": map[string]string{"url": url}})
	return nil
}

func (w *browserWorld) receivedOpen(action, url string) error {
	if err := w.received(action); err != nil {
		return err
	}
	var p struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.lastCmd.Params, &p)
	if p.URL != url {
		return fmt.Errorf("open_tab asked for %q", p.URL)
	}
	out, err := w.outcome()
	if err != nil {
		return err
	}
	if !strings.Contains(out, fmt.Sprintf(`"tabId":%d`, w.openedTab)) {
		return fmt.Errorf("the chat did not learn the new tab: %s", out)
	}
	return nil
}

func (w *browserWorld) clicksInOpened(_ string) error {
	w.tabID = w.openedTab
	w.call(map[string]any{"action": "click", "tab_id": w.openedTab, "params": map[string]any{"ref": "r1:3"}})
	return nil
}

func (w *browserWorld) receivedForOpened(action string) error {
	if err := w.receivedForTab(action); err != nil {
		return err
	}
	_, err := w.outcome()
	return err
}

func (w *browserWorld) close() {
	if w.ext != nil {
		_ = w.ext.Close()
	}
	if w.srv != nil {
		w.srv.Close()
	}
	_ = waitFor(func() bool { return !browser.Default().Connected("browser-bdd-0001") })
}

func waitFor(ok func() bool) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("timed out")
}

func TestBrowserFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "browser",
		Options: &godog.Options{
			Format: "progress", Paths: []string{"../../../features/browser.feature"}, TestingT: t, Strict: true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &browserWorld{t: t}
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				w.close()
				return ctx, err
			})
			sc.Step(`^Coddy for Browser connected to Coddy with the extension key$`, w.connected)
			sc.Step(`^the user shared the tab "([^"]*)" titled "([^"]*)"$`, w.shared)
			sc.Step(`^a chat lists the connected browsers$`, w.listed)
			sc.Step(`^the chat sees the tab "([^"]*)"$`, w.seesTab)
			sc.Step(`^the chat takes a snapshot of that tab$`, w.snapshot)
			sc.Step(`^the extension receives a "([^"]*)" command for that tab from that chat$`, w.receivedForTab)
			sc.Step(`^the chat receives the page text "([^"]*)"$`, w.pageText)
			sc.Step(`^a chat opens a tab on "([^"]*)"$`, w.opensTab)
			sc.Step(`^the extension receives an "([^"]*)" command for "([^"]*)"$`, w.receivedOpen)
			sc.Step(`^the chat clicks the link "([^"]*)" in the tab it opened$`, w.clicksInOpened)
			sc.Step(`^the extension receives a "([^"]*)" command for the opened tab from that chat$`, w.receivedForOpened)
			sc.Step(`^Coddy for Browser connected to a home Coddy that serves its browsers to other processes$`, w.connectedHome)
			sc.Step(`^a chat of the Coddy process "([^"]*)" lists the browsers of the home server$`, w.remoteListed)
			sc.Step(`^the chat sees the tab "([^"]*)" in a browser named after the home server$`, w.seesTabOnServer)
			sc.Step(`^the chat takes a snapshot of that tab in that browser$`, w.snapshotIn)
			sc.Step(`^the extension receives a "([^"]*)" command for that tab from the chat on "([^"]*)"$`, w.receivedFromNode)
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the browser feature failed")
	}
}
