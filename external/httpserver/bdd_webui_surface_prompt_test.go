//go:build http

package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// surfacePromptStand is a server whose runner records the surface block each
// turn ran with, the way the agent reads it into the system prompt.
type surfacePromptStand struct {
	ts      *httptest.Server
	mu      sync.Mutex
	blocks  []string
	lastSid string
	mgr     *session.Manager
}

func newSurfacePromptStand(t *testing.T) *surfacePromptStand {
	t.Helper()
	stand := &surfacePromptStand{}
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		stand.mu.Lock()
		stand.blocks = append(stand.blocks, st.GetSurfaceSystemPrompt())
		stand.lastSid = st.ID
		stand.mu.Unlock()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "stub"})
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: t.TempDir(), CWD: t.TempDir()},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	stand.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, stand.mgr, slog.Default(), t.TempDir())
	t.Cleanup(srv.Drain)
	stand.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(stand.ts.Close)
	return stand
}

func (s *surfacePromptStand) post(path, body string) error {
	res, err := http.Post(s.ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d %s", path, res.StatusCode, b)
	}
	return nil
}

func (s *surfacePromptStand) lastBlock() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.blocks) == 0 {
		return "\x00no turn ran"
	}
	return s.blocks[len(s.blocks)-1]
}

func TestWebUISurfacePromptFeature(t *testing.T) {
	var stand *surfacePromptStand
	suite := godog.TestSuite{
		Name: "web-ui-surface-prompt",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a Coddy HTTP server with a recording agent$`, func() { stand = newSurfacePromptStand(t) })
			ctx.Step(`^the web UI sends "([^"]+)" to /v1/responses$`, func(text string) error {
				return stand.post("/v1/responses", fmt.Sprintf(`{"model":"agent","input":%q,"stream":false,"metadata":{"surface":"webui"}}`, text))
			})
			ctx.Step(`^the turn's system prompt includes "([^"]+)"$`, func(want string) error {
				if !strings.Contains(stand.lastBlock(), want) {
					return fmt.Errorf("surface block %q lacks %q", stand.lastBlock(), want)
				}
				return nil
			})
			ctx.Step(`^the block names mermaid fences and LaTeX formulas$`, func() error {
				b := stand.lastBlock()
				for _, want := range []string{"`mermaid`", "`svg`", "LaTeX", "`$...$`"} {
					if !strings.Contains(b, want) {
						return fmt.Errorf("surface block lacks %s", want)
					}
				}
				return nil
			})
			ctx.Step(`^the session keeps no trace of the block after the turn$`, func() error {
				st := stand.mgr.SessionByID(stand.lastSid)
				if st == nil {
					return fmt.Errorf("session %s not found", stand.lastSid)
				}
				if got := st.GetSurfaceSystemPrompt(); got != "" {
					return fmt.Errorf("the block outlived the turn: %q", got)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/web_ui_surface_prompt.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI surface prompt scenarios failed")
	}
}

// Only the web UI asks for the block: an API client, the remote console and a
// metadata value nobody knows keep the plain prompt, on both chat routes.
func TestSurfacePromptOnlyForTheWebUI(t *testing.T) {
	stand := newSurfacePromptStand(t)
	cases := []struct {
		name, path, body string
		want             bool
	}{
		{"responses without metadata", "/v1/responses", `{"model":"agent","input":"hi","stream":false}`, false},
		{"responses with another surface", "/v1/responses", `{"model":"agent","input":"hi","stream":false,"metadata":{"surface":"console"}}`, false},
		{"responses with a non-string surface", "/v1/responses", `{"model":"agent","input":"hi","stream":false,"metadata":{"surface":1}}`, false},
		{"responses from the web UI, any case", "/v1/responses", `{"model":"agent","input":"hi","stream":false,"metadata":{"surface":"WebUI"}}`, true},
		{"chat completions without metadata", "/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"hi"}]}`, false},
		{"chat completions from the web UI", "/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"hi"}],"metadata":{"surface":"webui"}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := stand.post(c.path, c.body); err != nil {
				t.Fatal(err)
			}
			got := stand.lastBlock() == webUISurfacePrompt
			if got != c.want {
				t.Fatalf("surface block = %q, want web UI block: %v", stand.lastBlock(), c.want)
			}
		})
	}
}
