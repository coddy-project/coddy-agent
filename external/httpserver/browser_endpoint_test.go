//go:build http && browser

package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestBrowserKeyDollarsAuthenticateAfterSettingsReload(t *testing.T) {
	w := &configRoundTripWorld{}
	if err := w.startSpelledGateway(t); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.srv.Drain)
	key := strings.Repeat("k", 32) + "$CODDY_BDD_LOCAL_KEY-${CODDY_BDD_LOCAL_KEY}-$$"
	if err := w.saveBrowserKey(key); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"saved", "unrelated save"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "unrelated save" {
				if err := w.saveWithField("agent.max_turns", 42); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.LoadWithPaths(w.paths)
			if err != nil {
				t.Fatal(err)
			}
			srv, ts, _ := browserAuthServer(t, true)
			srv.ReplaceConfig(cfg)
			ext, err := browsertest.Connect(ts.URL, browsertest.Options{Key: key})
			if err != nil {
				t.Fatalf("the original key did not authenticate after reload: %v", err)
			}
			defer func() { _ = ext.Close() }()
			for _, wrong := range []string{
				strings.Repeat("k", 32) + "sk-local-sk-local-$",
				strings.ReplaceAll(key, "$", "$$"),
			} {
				other, err := browsertest.Connect(ts.URL, browsertest.Options{Key: wrong, ID: "browser-wrong-key"})
				if other != nil {
					_ = other.Close()
				}
				var rpcErr *browsertest.RPCError
				if !errors.As(err, &rpcErr) || rpcErr.Code != -32001 {
					t.Fatalf("a changed key must be refused as unauthorized, got %v", err)
				}
			}
		})
	}
}

// The extension reaches the HTTP server with its own key, past the token gate,
// and a new key in the settings disconnects an extension that held the old one.
func TestTheEndpointTakesTheExtensionKeyAndDropsItWhenTheKeyChanges(t *testing.T) {
	srv, ts, _ := browserAuthServer(t, true)
	if _, err := browsertest.Connect(ts.URL, browsertest.Options{Key: "web-ui-token"}); err == nil {
		t.Fatal("the HTTP API's token opened the extension's socket")
	}
	ext, err := browsertest.Connect(ts.URL, browsertest.Options{Key: browserTestKey, ID: "browser-endpoint-01"})
	if err != nil {
		t.Fatalf("the extension key did not pass the token gate: %v", err)
	}
	defer func() { _ = ext.Close() }()
	next := *srv.activeCfg()
	next.Browser.Token = strings.Repeat("n", 32)
	srv.ReplaceConfig(&next)
	select {
	case <-ext.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("an extension of the old key stayed connected")
	}
	var rpcErr *browsertest.RPCError
	if _, err := browsertest.Connect(ts.URL, browsertest.Options{Key: browserTestKey}); !errors.As(err, &rpcErr) {
		t.Fatalf("the old key: %v", err)
	}
	// browser.enable off: the endpoint is gone.
	next.Browser.Enable = false
	srv.ReplaceConfig(&next)
	resp, err := http.Get(ts.URL + "/browser/v1/connect")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the endpoint with browser.enable off: %d", resp.StatusCode)
	}
}

// The API other Coddy processes reach the browsers through is part of the
// HTTP API: its own credential opens it, the extension key does not, and it
// answers only while browser.serve_remote is on.
func TestTheBrowserAPIIsBehindTheGateAndServeRemote(t *testing.T) {
	srv, ts, _ := browserAuthServer(t, true)
	get := func(token string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/coddy/browser/v1/browsers", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Fatalf("no credential: %d", code)
	}
	if code := get(browserTestKey); code != http.StatusUnauthorized {
		t.Fatalf("the extension key opened the API: %d", code)
	}
	if code := get("web-ui-token"); code != http.StatusNotFound {
		t.Fatalf("serve_remote off: %d", code)
	}
	next := *srv.activeCfg()
	next.Browser.ServeRemote = true
	srv.ReplaceConfig(&next)
	if code := get("web-ui-token"); code != http.StatusOK {
		t.Fatalf("serve_remote on: %d", code)
	}
	next.Browser.Enable = false
	srv.ReplaceConfig(&next)
	if code := get("web-ui-token"); code != http.StatusNotFound {
		t.Fatalf("browser.enable off: %d", code)
	}
}
