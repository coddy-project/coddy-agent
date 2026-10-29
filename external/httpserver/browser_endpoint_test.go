//go:build http && browser

package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
)

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
