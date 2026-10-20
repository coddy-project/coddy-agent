//go:build http

package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A model served by a local ACP agent answers inside a Coddy session only: a
// direct completion of it is a client error that says so, and the agent is
// never started.
func TestDirectCompletionOfAnACPModelIsRefused(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Providers: []config.ProviderConfig{{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: filepath.Join(root, "no-such-agent")}}},
		Models:    []config.ModelEntry{{Model: "agent/default"}},
		Agent:     config.Agent{Model: "agent/default"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	requests := map[string]string{
		"/v1/chat/completions": `"messages":[{"role":"user","content":"hi"}]`,
		"/v1/responses":        `"input":"hi"`,
	}
	for path, input := range requests {
		for _, stream := range []string{"false", "true"} {
			body := `{"model":"agent/default","stream":` + stream + `,` + input + `}`
			res, err := http.Post(ts.URL+path, "application/json", bytes.NewReader([]byte(body)))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "acp agent") {
				t.Fatalf("%s stream %s: status %d: %s", path, stream, res.StatusCode, raw)
			}
			if res.Header.Get("X-Coddy-Session-ID") != "" {
				t.Fatalf("%s stream %s: a refused completion created a session", path, stream)
			}
		}
	}
}
