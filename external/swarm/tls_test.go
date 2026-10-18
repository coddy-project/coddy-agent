//go:build swarm

package swarm

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// HTTPS is supplied by the test edge, not by relay configuration.
func httpsRelay(t *testing.T, node *recordingNode) *httptest.Server {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.Clients = []config.SwarmClient{{Name: "bearer", Token: "bearer-secret", Scope: config.ScopeSharedModels, Nodes: []string{"nas02"}}}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	registerRecording(t, srv, "nas02", node)
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, c *http.Client, base, path, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

const mountModels = "/swarm/nodes/nas02/coddy/llm/models"

func TestAnEntryIsAuthenticatedByItsTokenOverHTTPS(t *testing.T) {
	node := newRecordingNode(t)
	ts := httpsRelay(t, node)
	for _, tc := range []struct {
		token string
		want  int
	}{{"bearer-secret", 200}, {"", 401}, {"wrong", 401}} {
		node.reset()
		code, _ := get(t, ts.Client(), ts.URL, mountModels, tc.token)
		if code != tc.want {
			t.Errorf("token %q: %d, want %d", tc.token, code, tc.want)
		}
		if (code == 200) != (len(node.requests()) > 0) {
			t.Errorf("status %d but node requests %v", code, node.requests())
		}
	}
	node.reset()
	get(t, ts.Client(), ts.URL, mountModels, "bearer-secret")
	for _, line := range node.requests() {
		if line != "GET /coddy/llm/models auth=Bearer node-secret q=" {
			t.Errorf("the node saw %q", line)
		}
	}
}

func TestHTTPSNeverOpensTheFullClassWithoutAToken(t *testing.T) {
	ts := httpsRelay(t, newRecordingNode(t))
	for _, path := range []string{"/swarm/nodes", "/swarm/sessions", "/swarm/topology"} {
		for _, token := range []string{"", "bearer-secret"} {
			if code, _ := get(t, ts.Client(), ts.URL, path, token); code == http.StatusOK {
				t.Errorf("GET %s with token %q opened the full class", path, token)
			}
		}
	}
	if code, _ := get(t, ts.Client(), ts.URL, "/swarm/nodes", fullToken); code != http.StatusOK {
		t.Errorf("the full token over HTTPS = %d", code)
	}
}

func TestATokenOverHTTPSWorksOverHTTP2(t *testing.T) {
	ts := httpsRelay(t, newRecordingNode(t))
	req, err := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer bearer-secret")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.ProtoMajor != 2 || res.StatusCode != http.StatusOK {
		t.Errorf("over HTTP/%d: %d, want HTTP/2 200", res.ProtoMajor, res.StatusCode)
	}
}
