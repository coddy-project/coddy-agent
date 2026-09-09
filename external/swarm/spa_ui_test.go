//go:build swarm && ui

package swarm

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A relay is a place people open in a browser, so it serves the same SPA a node
// does. Without this the relay's own address answers 404 and the swarm map is
// only reachable by pointing some other node's UI at it.
func TestRelayServesTheSPAAtItsRoot(t *testing.T) {
	cfg := &config.Config{Swarm: config.SwarmConfig{Name: "outer", AuthToken: "tok"}}
	srv, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// No credential: a browser has none until the page it is loading asks for one.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / status %d body %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "<title>Coddy Agent</title>") {
		t.Fatalf("relay root is not the SPA index: %q", b)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control %q, want no-cache", cc)
	}

	// The API behind it stays credentialed.
	nres, err := http.Get(ts.URL + "/swarm/nodes")
	if err != nil {
		t.Fatal(err)
	}
	_ = nres.Body.Close()
	if nres.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /swarm/nodes without a token: status %d, want 401", nres.StatusCode)
	}
}

func TestRelaySPAHonoursUIDisabled(t *testing.T) {
	off := false
	cfg := &config.Config{
		Swarm: config.SwarmConfig{Name: "outer"},
		UI:    config.UIConfig{Enabled: &off},
	}
	srv, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("ui disabled GET / status %d want 404: %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "<title>Coddy Agent</title>") {
		t.Fatal("ui.enabled false still served the SPA")
	}
}
