//go:build http

package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newLoopbackGuardServer(t *testing.T, httpCfg config.HTTPServerConfig) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Agent:      config.Agent{Model: "openai/gpt-4o"},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		HTTPServer: httpCfg,
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	log := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, runner, log, t.TempDir(), nil)
	ts := httptest.NewServer(New(cfg, mgr, log, t.TempDir()).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func loopbackGuardDo(t *testing.T, ts *httptest.Server, method, path, host, origin string) int {
	t.Helper()
	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func TestLoopbackGuard_NoCredential(t *testing.T) {
	ts := newLoopbackGuardServer(t, config.HTTPServerConfig{})
	own := strings.TrimPrefix(ts.URL, "http://")
	port := own[strings.LastIndex(own, ":"):]

	cases := []struct {
		name, method, path, host, origin string
		refused                          int
	}{
		{"own address", http.MethodGet, "/v1/models", "", "", 0},
		{"localhost name", http.MethodGet, "/v1/models", "localhost" + port, "", 0},
		{"subdomain of localhost", http.MethodGet, "/v1/models", "app.localhost" + port, "", 0},
		{"ipv6 loopback name", http.MethodGet, "/v1/models", "[::1]" + port, "", 0},
		{"foreign host name", http.MethodGet, "/v1/models", "attacker.example" + port, "", http.StatusMisdirectedRequest},
		{"foreign host on a write", http.MethodPost, "/v1/chat/completions", "attacker.example", "", http.StatusMisdirectedRequest},
		{"write without origin", http.MethodPost, "/v1/chat/completions", "", "", 0},
		{"write from own origin", http.MethodPost, "/v1/chat/completions", "", ts.URL, 0},
		{"write from another site", http.MethodPost, "/v1/chat/completions", "", "https://attacker.example", http.StatusForbidden},
		{"write from another loopback port", http.MethodPost, "/v1/chat/completions", "", "http://127.0.0.1:1", http.StatusForbidden},
		{"read from another site", http.MethodGet, "/v1/models", "", "https://attacker.example", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := loopbackGuardDo(t, ts, tc.method, tc.path, tc.host, tc.origin)
			if tc.refused != 0 && got != tc.refused {
				t.Fatalf("status %d, want %d", got, tc.refused)
			}
			if tc.refused == 0 && (got == http.StatusMisdirectedRequest || got == http.StatusForbidden) {
				t.Fatalf("status %d, want the request to pass the guard", got)
			}
		})
	}
}

func TestLoopbackGuard_CORSAdmittedOrigin(t *testing.T) {
	ts := newLoopbackGuardServer(t, config.HTTPServerConfig{
		CORS: config.HTTPCORSConfig{Enabled: true, AllowedOrigins: []string{"http://localhost:5201"}},
	})
	if got := loopbackGuardDo(t, ts, http.MethodPost, "/v1/chat/completions", "", "http://localhost:5201"); got == http.StatusForbidden {
		t.Fatalf("an origin httpserver.cors admits was refused")
	}
	if got := loopbackGuardDo(t, ts, http.MethodPost, "/v1/chat/completions", "", "http://localhost:5202"); got != http.StatusForbidden {
		t.Fatalf("status %d for an origin cors does not list, want 403", got)
	}
}

func TestLoopbackGuard_CredentialLeavesItToAuth(t *testing.T) {
	ts := newLoopbackGuardServer(t, config.HTTPServerConfig{AuthToken: "secret"})
	if got := loopbackGuardDo(t, ts, http.MethodGet, "/v1/models", "coddy.internal", ""); got != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 from the auth gate", got)
	}
	if got := loopbackGuardDo(t, ts, http.MethodPost, "/v1/chat/completions", "", "https://attacker.example"); got != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 from the auth gate", got)
	}
}

func TestIsLoopbackName(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost":         true,
		"localhost.":        true,
		"app.localhost":     true,
		"127.0.0.1":         true,
		"127.1.2.3":         true,
		"::1":               true,
		"0.0.0.0":           false,
		"192.168.1.10":      false,
		"localhost.example": false,
		"example.com":       false,
		"":                  false,
	} {
		if got := isLoopbackName(hostName(host)); got != want {
			t.Errorf("isLoopbackName(%q) = %v, want %v", host, got, want)
		}
	}
}
