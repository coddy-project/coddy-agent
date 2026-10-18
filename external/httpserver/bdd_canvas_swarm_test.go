//go:build http && swarm

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

	"github.com/cucumber/godog"

	swarmserver "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type canvasSwarmState struct {
	root, workspace, sessionID string
	node, relay                *httptest.Server
	relayToken, nodeToken      string

	mu        sync.Mutex
	frameAuth []string

	frameURL string
	page     *http.Response
	pageBody string
}

func (s *canvasSwarmState) close() {
	for _, srv := range []*httptest.Server{s.relay, s.node} {
		if srv != nil {
			srv.Close()
		}
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
	*s = canvasSwarmState{}
}

func (s *canvasSwarmState) nodeBehindRelay() error {
	root, err := os.MkdirTemp("", "coddy-canvas-swarm-*")
	if err != nil {
		return err
	}
	s.root = root
	s.workspace = filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(s.workspace, "site"), 0o755); err != nil {
		return err
	}
	for rel, body := range map[string]string{
		"site/index.html": `<!doctype html><html><head><title>Shop</title><script type="module" src="app.js"></script></head><body>shop</body></html>`,
		"site/app.js":     "export const ok = true;\n",
	} {
		if err := os.WriteFile(filepath.Join(s.workspace, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	s.relayToken, s.nodeToken = "relay-client-token", "node-service-token"
	cfg := &config.Config{
		Paths:      config.Paths{Home: filepath.Join(root, "home"), CWD: s.workspace},
		Models:     []config.ModelEntry{{Model: "fake/model"}},
		Agent:      config.Agent{Model: "fake/model"},
		HTTPServer: config.HTTPServerConfig{AuthToken: s.nodeToken},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}, log, s.workspace, store)
	nodeServer := New(cfg, mgr, log, s.workspace)
	s.node = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, canvasFilePrefix) {
			s.mu.Lock()
			s.frameAuth = append(s.frameAuth, r.Header.Get("Authorization"))
			s.mu.Unlock()
		}
		nodeServer.Handler().ServeHTTP(w, r)
	}))
	relayCfg := &config.Config{}
	relayCfg.Swarm.Host = "127.0.0.1"
	relayCfg.Swarm.AuthToken = s.relayToken
	relayCfg.Swarm.PairingTokens = []string{"relay-pair-token"}
	relayServer, err := swarmserver.New(relayCfg, log)
	if err != nil {
		return err
	}
	s.relay = httptest.NewServer(relayServer.Handler())
	registration, _ := json.Marshal(swarmdto.RegisterRequest{
		Name: "nas02", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: s.node.URL, InstanceUUID: "canvas-swarm-node", Token: s.nodeToken, Version: "test",
	})
	req, _ := http.NewRequest(http.MethodPost, s.relay.URL+"/swarm/register", bytes.NewReader(registration))
	req.Header.Set("Authorization", "Bearer relay-pair-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("register node: %s", res.Status)
	}
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	s.sessionID = created.SessionID
	return nil
}

func (s *canvasSwarmState) mounted(path string) string {
	return s.relay.URL + swarmdto.MountPath + "nas02" + path
}

func (s *canvasSwarmState) putOnCanvas(path string) error {
	req, _ := http.NewRequest(http.MethodPost, s.mounted("/coddy/sessions/"+s.sessionID+"/canvas/frames"), strings.NewReader(`{"path":"`+path+`"}`))
	req.Header.Set("Authorization", "Bearer "+s.relayToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Frame struct {
			URL string `json:"url"`
		} `json:"frame"`
	}
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.Frame.URL == "" {
		return fmt.Errorf("put on canvas: %s %s", res.Status, raw)
	}
	s.frameURL = out.Frame.URL
	return nil
}

func (s *canvasSwarmState) get(target, origin string) (*http.Response, string, error) {
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res, string(raw), nil
}

func (s *canvasSwarmState) loadFrame() error {
	res, body, err := s.get(s.mounted(s.frameURL), "")
	if err != nil {
		return err
	}
	s.page, s.pageBody = res, body
	return nil
}

func (s *canvasSwarmState) pageArrives() error {
	if s.page.StatusCode != http.StatusOK || !strings.Contains(s.pageBody, `<script src=".coddy/bridge.js"></script>`) {
		return fmt.Errorf("page %s: %s", s.page.Status, s.pageBody)
	}
	if csp := s.page.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox allow-scripts") {
		return fmt.Errorf("the node's sandbox policy did not arrive: %q", csp)
	}
	return nil
}

func (s *canvasSwarmState) moduleArrives() error {
	base := strings.TrimSuffix(s.frameURL, "index.html")
	res, body, err := s.get(s.mounted(base+"app.js"), "null")
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "export const ok") {
		return fmt.Errorf("module %s %s", res.Status, body)
	}
	if got := res.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
		return fmt.Errorf("CORS %v", got)
	}
	bridge, _, err := s.get(s.mounted(base+".coddy/bridge.js"), "null")
	if err != nil {
		return err
	}
	if bridge.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge %s", bridge.Status)
	}
	return nil
}

func (s *canvasSwarmState) noRelayCredential() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frameAuth) < 3 {
		return fmt.Errorf("the node saw %d frame requests", len(s.frameAuth))
	}
	for _, a := range s.frameAuth {
		if a != "" {
			return fmt.Errorf("the relay vouched for a frame request with %q", a)
		}
	}
	return nil
}

func (s *canvasSwarmState) apiRefused() error {
	res, body, err := s.get(s.mounted("/coddy/sessions"), "null")
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusForbidden {
		return fmt.Errorf("the page reached the node's API: %s %s", res.Status, body)
	}
	return nil
}

func initializeCanvasSwarmScenario(sc *godog.ScenarioContext) {
	s := &canvasSwarmState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a node that asks for a token, behind a relay that asks for a client token, with a shop page in its workspace$`, s.nodeBehindRelay)
	sc.Step(`^the person put "([^"]*)" on the node's canvas through the relay$`, s.putOnCanvas)
	sc.Step(`^the browser loads the frame's address through the relay without a token$`, s.loadFrame)
	sc.Step(`^the page arrives with the bridge under the node's sandbox policy$`, s.pageArrives)
	sc.Step(`^the frame's module script arrives for an opaque origin$`, s.moduleArrives)
	sc.Step(`^the node saw no credential of the relay on the frame's requests$`, s.noRelayCredential)
	sc.Step(`^the page's request to the node's API through the relay is refused$`, s.apiRefused)
}

func TestCanvasSwarmFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "canvas-swarm",
		ScenarioInitializer: initializeCanvasSwarmScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features/canvas_swarm.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("canvas swarm feature suite failed")
	}
}
