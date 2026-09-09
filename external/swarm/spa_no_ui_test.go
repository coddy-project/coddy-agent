//go:build swarm && !ui

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

// Built without the ui tag the relay has no SPA to serve. It should say that,
// not answer a bare 404 that reads like a wrong address.
func TestRelayWithoutUISaysSo(t *testing.T) {
	cfg := &config.Config{Swarm: config.SwarmConfig{Name: "outer"}}
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
		t.Fatalf("GET / status %d want 404", res.StatusCode)
	}
	if !strings.Contains(string(b), `-tags "swarm ui"`) {
		t.Fatalf("expected the rebuild hint, got %q", b)
	}
}
