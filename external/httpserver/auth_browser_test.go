//go:build http

package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const browserTestKey = "0123456789abcdef0123456789abcdef"

func browserAuthServer(t *testing.T, browserOn bool) (*Server, *httptest.Server, *http.Client) {
	t.Helper()
	cfg := &config.Config{
		Paths:      config.Paths{Home: t.TempDir(), CWD: t.TempDir()},
		HTTPServer: config.HTTPServerConfig{AuthToken: "web-ui-token"},
		Browser:    config.BrowserConfig{Enable: browserOn, Token: browserTestKey},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), t.TempDir(), &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return srv, ts, &http.Client{Jar: jar}
}

func browserReq(t *testing.T, c *http.Client, method, url, body, site string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func ticketFor(browserID, key string) string {
	return browser.IssuePanelTicket(browser.PanelGrant{BrowserID: browserID, KeyDigest: browser.KeyDigest(key)}, time.Now())
}

func TestAPanelTicketSignsInOnceAndTheSessionOpensTheGate(t *testing.T) {
	_, ts, c := browserAuthServer(t, true)
	ticket := ticketFor("browser-auth-0001", browserTestKey)
	if code, _ := browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+ticket+`"}`, "cross-site"); code != http.StatusForbidden {
		t.Fatalf("a cross-site sign-in: %d", code)
	}
	if code, body := browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+ticket+`"}`, "same-origin"); code != http.StatusOK {
		t.Fatalf("sign-in: %d %s", code, body)
	}
	if code, _ := browserReq(t, c, http.MethodGet, ts.URL+"/coddy/sessions", "", "same-origin"); code != http.StatusOK {
		t.Fatalf("the panel's session did not open the gate: %d", code)
	}
	if code, _ := browserReq(t, &http.Client{}, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+ticket+`"}`, "same-origin"); code != http.StatusUnauthorized {
		t.Fatalf("a used ticket: %d", code)
	}
	// Logging out ends the panel's session too.
	browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/logout", `{}`, "same-origin")
	if code, _ := browserReq(t, c, http.MethodGet, ts.URL+"/coddy/sessions", "", "same-origin"); code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", code)
	}
}

func TestANewKeyEndsThePanelSessionsAndRefusesOldTickets(t *testing.T) {
	srv, ts, c := browserAuthServer(t, true)
	if code, _ := browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+ticketFor("browser-auth-0002", browserTestKey)+`"}`, "same-origin"); code != http.StatusOK {
		t.Fatalf("sign-in: %d", code)
	}
	stale := ticketFor("browser-auth-0002", browserTestKey)
	next := *srv.activeCfg()
	next.Browser.Token = strings.Repeat("n", 32)
	srv.ReplaceConfig(&next)
	if code, _ := browserReq(t, c, http.MethodGet, ts.URL+"/coddy/sessions", "", "same-origin"); code != http.StatusUnauthorized {
		t.Fatalf("a session of the old key: %d", code)
	}
	if code, _ := browserReq(t, &http.Client{}, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+stale+`"}`, "same-origin"); code != http.StatusUnauthorized {
		t.Fatalf("a ticket of the old key: %d", code)
	}
}

func TestPanelSignInNeedsTheListener(t *testing.T) {
	_, ts, c := browserAuthServer(t, false)
	if code, _ := browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/browser", `{"ticket":"`+ticketFor("browser-auth-0003", browserTestKey)+`"}`, "same-origin"); code != http.StatusBadRequest {
		t.Fatalf("sign-in without the listener: %d", code)
	}
	if _, body := browserReq(t, c, http.MethodGet, ts.URL+"/coddy/auth/me", "", "same-origin"); !strings.Contains(body, `"browser_login":false`) {
		t.Fatalf("me: %s", body)
	}
	if code, _ := browserReq(t, c, http.MethodPost, ts.URL+"/coddy/auth/browser", `nonsense`, "same-origin"); code != http.StatusBadRequest {
		t.Fatalf("a bad body: %d", code)
	}
}

func TestAPanelTurnIgnoresMetadataItCannotUse(t *testing.T) {
	srv, _, _ := browserAuthServer(t, true)
	dir := t.TempDir()
	panel, err := srv.browserPanelTurn([]byte(`{"browser":{"browser_id":"browser-auth-0004","tab_id":3}}`), dir)
	if err != nil || !strings.Contains(panel.TurnContext, `"tab_id":3`) || strings.Contains(panel.SystemPrompt, `"tab_id"`) {
		t.Fatalf("a panel turn: %+v %v", panel, err)
	}
	// Anything else adds nothing and takes back the default tab of the turn
	// before.
	for _, raw := range []string{``, `{}`, `{"browser":"tab"}`, `{"browser":{"browser_id":"x","tab_id":1}}`, `not json`} {
		if _, err := srv.browserPanelTurn([]byte(`{"browser":{"browser_id":"browser-auth-0004","tab_id":3}}`), dir); err != nil {
			t.Fatal(err)
		}
		if p, err := srv.browserPanelTurn([]byte(raw), dir); p != (browser.PanelTurn{}) || err != nil {
			t.Fatalf("%q gave %+v %v", raw, p, err)
		}
		if browser.PanelTabOf(dir, "browser-auth-0004") != 0 {
			t.Fatalf("%q left the tab of the turn before", raw)
		}
	}
	// With the endpoint off, a panel turn changes nothing.
	off, _, _ := browserAuthServer(t, false)
	if p, err := off.browserPanelTurn([]byte(`{"browser":{"browser_id":"browser-auth-0004","tab_id":3}}`), t.TempDir()); p != (browser.PanelTurn{}) || err != nil {
		t.Fatalf("the endpoint off: %+v %v", p, err)
	}
}
