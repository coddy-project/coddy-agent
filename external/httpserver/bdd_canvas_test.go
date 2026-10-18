//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/canvas"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type canvasFeature struct {
	root   string
	cwd    string
	sid    string
	model  *httptest.Server
	srv    *Server
	ts     *httptest.Server
	cancel context.CancelFunc

	mu     sync.Mutex
	events []sseFrame

	frame canvasFrameJSON
}

func (s *canvasFeature) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-canvas-bdd-*")
	if err != nil {
		return err
	}
	*s = canvasFeature{root: root}
	return nil
}

func (s *canvasFeature) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.ts != nil {
		s.ts.Close()
	}
	if s.srv != nil {
		s.srv.Drain()
	}
	if s.model != nil {
		s.model.Close()
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
}

func (s *canvasFeature) chatWithShopPage(dir string) error {
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{
		{Match: "show the shop on the canvas", Tool: &llmstub.ToolCall{Name: "canvas_show", Arguments: json.RawMessage(`{"path":"` + dir + `/index.html","title":"Shop"}`)}, Answer: "The shop is on the canvas."},
		{Match: "check the canvas", Tool: &llmstub.ToolCall{Name: "canvas_inspect", Arguments: json.RawMessage(`{}`)}, Answer: "The cart script throws."},
	}}
	s.model = httptest.NewServer(stub.Handler())
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "work")
	for _, d := range []string{home, filepath.Join(s.cwd, dir)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		dir + "/index.html": `<!doctype html><html><head><title>Shop</title><script type="module" src="app.js"></script></head><body><button id="add">Add to cart</button></body></html>`,
		dir + "/app.js":     "document.getElementById('add').onclick = () => cart.push(1);\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(s.cwd, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: s.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
	}
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, mgr, log, s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.sid = res.SessionID
	return nil
}

func (s *canvasFeature) browserFollowsEvents() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return fmt.Errorf("GET /coddy/events: %s", res.Status)
	}
	ready := make(chan struct{})
	go func() {
		defer func() { _ = res.Body.Close() }()
		var once sync.Once
		readSSEStream(res.Body, func(f sseFrame) {
			if f.event == "ready" {
				once.Do(func() { close(ready) })
			}
			s.mu.Lock()
			s.events = append(s.events, f)
			s.mu.Unlock()
		})
	}()
	select {
	case <-ready:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the events stream never became ready")
	}
}

func (s *canvasFeature) personAsks(text string) error {
	body, _ := json.Marshal(map[string]any{"model": "agent", "input": text, "stream": true})
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the turn answered %s:\n%s", res.Status, raw)
	}
	return nil
}

func (s *canvasFeature) readBoard() (canvasBoardJSON, error) {
	var b canvasBoardJSON
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/canvas")
	if err != nil {
		return b, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return b, fmt.Errorf("GET canvas: %s %s", res.Status, raw)
	}
	return b, json.NewDecoder(res.Body).Decode(&b)
}

func (s *canvasFeature) canvasHoldsOneFrame(path string, width, height int) error {
	b, err := s.readBoard()
	if err != nil {
		return err
	}
	if len(b.Frames) != 1 {
		return fmt.Errorf("the canvas holds %d frames: %+v", len(b.Frames), b.Frames)
	}
	f := b.Frames[0]
	if f.Path != path || f.Width != width || f.Height != height || f.Title != "Shop" || f.AddedBy != "agent" {
		return fmt.Errorf("frame %+v", f)
	}
	s.frame = f
	return nil
}

func (s *canvasFeature) eventsAnnouncedAdded() error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, f := range s.events {
			if f.event == "canvas_changed" && strings.Contains(f.data, s.frame.ID) && strings.Contains(f.data, `"reason":"added"`) && strings.Contains(f.data, s.sid) {
				s.mu.Unlock()
				return nil
			}
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("no canvas_changed event for %s", s.frame.ID)
}

func (s *canvasFeature) get(rel string, headers map[string]string) (*http.Response, string, error) {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+rel, nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res, string(raw), nil
}

func (s *canvasFeature) frameServesThePage() error {
	res, body, err := s.get(s.frame.URL, nil)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<script src=".coddy/bridge.js"></script>`) || !strings.Contains(body, "Add to cart") {
		return fmt.Errorf("page %s:\n%s", res.Status, body)
	}
	csp := res.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		return fmt.Errorf("CSP %q", csp)
	}
	if res.Header.Get("Referrer-Policy") != "no-referrer" {
		return fmt.Errorf("referrer policy %q", res.Header.Get("Referrer-Policy"))
	}
	return nil
}

func (s *canvasFeature) moduleLoadsFromOpaqueOrigin() error {
	base := strings.TrimSuffix(s.frame.URL, "index.html")
	res, body, err := s.get(base+"app.js", map[string]string{"Origin": "null"})
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(body, "cart.push") {
		return fmt.Errorf("module %s %v", res.Status, res.Header)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		return fmt.Errorf("module type %q", res.Header.Get("Content-Type"))
	}
	return nil
}

func (s *canvasFeature) personAsked(text string) error {
	if err := s.personAsks(text); err != nil {
		return err
	}
	b, err := s.readBoard()
	if err != nil {
		return err
	}
	if len(b.Frames) != 1 {
		return fmt.Errorf("the canvas holds %d frames", len(b.Frames))
	}
	s.frame = b.Frames[0]
	return nil
}

func (s *canvasFeature) browserReportsError(message string) error {
	body, _ := json.Marshal(map[string]any{
		"stamp": s.frame.Stamp, "loaded": true, "title": "Shop",
		"console": []map[string]any{{"level": "error", "message": message, "source": "app.js", "line": 1}},
	})
	res, err := http.Post(s.ts.URL+"/coddy/sessions/"+s.sid+"/canvas/frames/"+s.frame.ID+"/report", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("report: %s", res.Status)
	}
	return nil
}

func (s *canvasFeature) agentReadPageOutput(message string) error {
	defer canvas.DefaultReports().Forget(s.sid, s.frame.ID)
	st := s.srv.mgr.SessionByID(s.sid)
	if st == nil {
		return fmt.Errorf("no session")
	}
	for _, m := range st.GetMessages() {
		if m.Role != llm.RoleTool || !strings.Contains(m.Content, "<page_console") {
			continue
		}
		if strings.Contains(m.Content, message) && strings.Contains(m.Content, "untrusted output of the page") && strings.Contains(m.Content, "for the current files") {
			return nil
		}
		return fmt.Errorf("canvas_inspect result:\n%s", m.Content)
	}
	return fmt.Errorf("no canvas_inspect result quoting the page's console")
}

func (s *canvasFeature) personRemovesFrame() error {
	req, _ := http.NewRequest(http.MethodDelete, s.ts.URL+"/coddy/sessions/"+s.sid+"/canvas/frames/"+s.frame.ID, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("remove: %s", res.Status)
	}
	return nil
}

func (s *canvasFeature) addressRefused() error {
	res, body, err := s.get(s.frame.URL, nil)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("the removed frame's address answered %s:\n%s", res.Status, body)
	}
	return nil
}

func (s *canvasFeature) stillInWorkspace(rel string) error {
	_, err := os.Stat(filepath.Join(s.cwd, filepath.FromSlash(rel)))
	return err
}

func initializeCanvasScenario(sc *godog.ScenarioContext) {
	s := &canvasFeature{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a chat whose agent is a scripted model, in a workspace with a shop page in "([^"]*)"$`, s.chatWithShopPage)
	sc.Step(`^a browser following the server's events$`, s.browserFollowsEvents)
	sc.Step(`^the person asks "([^"]*)"$`, s.personAsks)
	sc.Step(`^the person asked "([^"]*)"$`, s.personAsked)
	sc.Step(`^the canvas of the chat holds one frame for "([^"]*)" at (\d+)x(\d+)$`, s.canvasHoldsOneFrame)
	sc.Step(`^the events stream announced the frame as added$`, s.eventsAnnouncedAdded)
	sc.Step(`^the frame's address serves the page with the bridge under a sandbox policy$`, s.frameServesThePage)
	sc.Step(`^the frame's module script loads from the frame's opaque origin$`, s.moduleLoadsFromOpaqueOrigin)
	sc.Step(`^the browser reports the console error "([^"]*)" for the frame's current files$`, s.browserReportsError)
	sc.Step(`^the agent read "([^"]*)" as untrusted output of the page$`, s.agentReadPageOutput)
	sc.Step(`^the person takes the frame off the canvas$`, s.personRemovesFrame)
	sc.Step(`^the frame's address is refused$`, s.addressRefused)
	sc.Step(`^"([^"]*)" is still in the workspace$`, s.stillInWorkspace)
}

func TestCanvasFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "canvas",
		ScenarioInitializer: initializeCanvasScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features/canvas.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("canvas feature suite failed")
	}
}
