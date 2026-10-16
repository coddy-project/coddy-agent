//go:build metrics

package metrics

// Godog harness for features/metrics_exporter.feature: a real serve.Runtime
// over a temporary home, the exporter run as a subsystem by a serve.Supervisor
// on a free port with a token, and a session that runs one turn through the
// real llm.NewProvider against tgfake's scripted model (llmstub), which reads a
// file with the read tool before it answers. Nothing here builds the HTTP API:
// the exporter has a listener of its own.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

type exporterState struct {
	root  string
	addr  string
	token string

	rt        *serve.Runtime
	collector *Collector
	model     *httptest.Server
	asked     atomic.Int64 // requests the model received
	cancel    context.CancelFunc
	stopped   chan error

	before, after string
}

func (s *exporterState) close() {
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.stopped:
		case <-time.After(10 * time.Second):
		}
		s.cancel = nil
	}
	if s.collector != nil {
		s.collector.Close()
		s.collector = nil
	}
	if s.model != nil {
		s.model.Close()
		s.model = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.rt = nil
	s.before, s.after = "", ""
}

// serveWithMetrics starts a runtime and runs the exporter in it as the
// supervisor runs any subsystem, next to a keeper that stands in for the
// surface a real process serves.
func (s *exporterState) serveWithMetrics(token string) error {
	root, err := os.MkdirTemp("", "coddy-metrics-bdd-*")
	if err != nil {
		return err
	}
	s.root, s.token = root, token
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	_ = ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: cwd}}
	cfg.Metrics = config.MetricsConfig{Enable: true, Host: "127.0.0.1", Port: port, Token: token}
	log := slog.New(slog.DiscardHandler)
	s.rt = &serve.Runtime{}
	if err := s.rt.Init(serve.Options{
		CLI: config.CLIPaths{Home: home, CWD: cwd}, SessionsRoot: filepath.Join(root, "sessions"),
		NeedsSessions: true, Log: log, Cfg: cfg,
	}); err != nil {
		return err
	}
	s.collector = NewCollector(Source{
		Status: s.rt.Status, Cfg: s.rt.Cfg, Mgr: s.rt.Mgr, AddConfigObserver: s.rt.AddConfigObserver, Log: log,
	})

	keeper := serve.Subsystem{
		Kind: serve.KindScheduler, Available: true,
		Enabled: func(*config.Config) bool { return true },
		Run: func(ctx context.Context, _ *config.Config) error {
			<-ctx.Done()
			return nil
		},
	}
	exporter := serve.Subsystem{
		Kind: serve.KindMetrics, ConfigKey: "metrics.enable", BuildTag: "metrics", Available: true, Auxiliary: true,
		Enabled:     func(c *config.Config) bool { return c.Metrics.Enable },
		Fingerprint: func(c *config.Config) string { return c.Metrics.ListenAddr() },
		Run: func(ctx context.Context, c *config.Config) error {
			return Serve(ctx, Options{Addr: c.Metrics.ListenAddr(), Cfg: s.rt.Cfg, Log: s.rt.Log, Collector: s.collector})
		},
	}
	sup := serve.NewSupervisor(log, []serve.Subsystem{keeper, exporter})
	s.rt.SetSupervisor(sup)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.stopped = make(chan error, 1)
	go func() { s.stopped <- sup.Run(ctx, cfg, make(chan *config.Config)) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", s.addr, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the exporter never listened on %s", s.addr)
}

// providerWithModel serves the model with llmstub and declares it in the live
// configuration, the way a settings save does.
func (s *exporterState) providerWithModel(provider, model string) error {
	name, api, err := config.SplitModelRef(model)
	if err != nil {
		return err
	}
	if name != provider {
		return fmt.Errorf("the model %q is not the provider %q's", model, provider)
	}
	stub := &llmstub.Server{Model: api, StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{
		{Match: "SECRET-PROMPT", Answer: "A scripted answer.", Tool: &llmstub.ToolCall{Name: "read", Arguments: []byte(`{"path":"notes.txt"}`)}},
	}}
	handler := stub.Handler()
	s.model = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.asked.Add(1)
		handler.ServeHTTP(w, r)
	}))

	next := *s.rt.Cfg()
	next.Providers = []config.ProviderConfig{{Name: provider, Type: "openai", APIBase: s.model.URL + "/v1", APIKey: "sk-stub"}}
	next.Models = []config.ModelEntry{{Model: model, MaxContextTokens: 131072}}
	next.Agent.Model = model
	s.rt.ReplaceConfig(&next)
	return nil
}

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (noopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// aSessionRanOneTurn takes a scrape first, so what the scenario compares is
// what the turn did.
func (s *exporterState) aSessionRanOneTurn(title, folder, prompt string) error {
	var err error
	if s.before, err = s.scrape("/metrics", s.token); err != nil {
		return err
	}
	dir := filepath.Join(s.root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("SECRET-NOTES\n"), 0o644); err != nil {
		return err
	}
	ctx := context.Background()
	res, err := s.rt.Mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: dir})
	if err != nil {
		return err
	}
	st := s.rt.Mgr.SessionByID(res.SessionID)
	if st == nil {
		return fmt.Errorf("session %s is not live", res.SessionID)
	}
	st.SetTitlePinned(title)
	if _, err := s.rt.Mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: prompt}},
	}, noopSender{}, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.rt.Mgr.ActiveTurnSessionIDs()) > 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("the turn is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (s *exporterState) scrape(path, token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+s.addr+path, nil)
	if err != nil {
		return "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s\n%s", path, resp.Status, body)
	}
	return string(body), nil
}

// prometheusScrapes also holds that a scrape asks the provider nothing.
func (s *exporterState) prometheusScrapes(path, token string) (err error) {
	asked := s.asked.Load()
	if s.after, err = s.scrape(path, token); err != nil {
		return err
	}
	if n := s.asked.Load() - asked; n != 0 {
		return fmt.Errorf("the scrape sent %d requests to the provider", n)
	}
	return nil
}

// total is the value of a series, or zero when the scrape holds none.
func total(body, name string, labels map[string]string) float64 {
	v, _ := find(body, name, labels)
	return v
}

func (s *exporterState) buildInfoAndAttemptsRose(info, attempts, provider, model, outcome string, want float64) error {
	if got := total(s.after, info, nil); got != 1 {
		return fmt.Errorf("%s = %v, want 1", info, got)
	}
	labels := map[string]string{"provider": provider, "model": model, "outcome": outcome}
	if got := total(s.after, attempts, labels) - total(s.before, attempts, labels); got != want {
		return fmt.Errorf("%s%v rose by %v, want %v\nbefore:\n%s\nafter:\n%s", attempts, labels, got, want, s.before, s.after)
	}
	return nil
}

func (s *exporterState) toolCallsRose(name, tool, outcome string, want float64) error {
	labels := map[string]string{"tool": tool, "outcome": outcome}
	if got := total(s.after, name, labels) - total(s.before, name, labels); got != want {
		return fmt.Errorf("%s%v rose by %v, want %v\nafter:\n%s", name, labels, got, want, s.after)
	}
	return nil
}

func (s *exporterState) rose(name string, want float64) error {
	if got := total(s.after, name, nil) - total(s.before, name, nil); got != want {
		return fmt.Errorf("%s rose by %v, want %v", name, got, want)
	}
	return nil
}

func (s *exporterState) eachRose(a, b string, want float64) error {
	for _, name := range []string{a, b} {
		if got := total(s.after, name, nil) - total(s.before, name, nil); got != want {
			return fmt.Errorf("%s rose by %v, want %v", name, got, want)
		}
	}
	return nil
}

func (s *exporterState) isPresent(name string) error {
	if _, ok := find(s.after, name, nil); !ok {
		return fmt.Errorf("%s is not in the scrape:\n%s", name, s.after)
	}
	return nil
}

func (s *exporterState) containsNoneOf(a, b, c string) error {
	for _, secret := range []string{a, b, c, "SECRET-NOTES", "notes.txt"} {
		if strings.Contains(s.after, secret) {
			return fmt.Errorf("the scrape contains %q", secret)
		}
	}
	return nil
}

func initializeExporterScenario(sc *godog.ScenarioContext) {
	s := &exporterState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^coddy serve built with metrics, the exporter on a free port with the token "([^"]*)"$`, s.serveWithMetrics)
	sc.Step(`^a provider "([^"]*)" served by llmstub with the model "([^"]*)" declared$`, s.providerWithModel)
	sc.Step(`^a session titled "([^"]*)" in the folder "([^"]*)" ran one turn with "([^"]*)" that read a file$`, s.aSessionRanOneTurn)
	sc.Step(`^Prometheus scrapes "([^"]*)" with the token "([^"]*)"$`, s.prometheusScrapes)
	sc.Step(`^"([^"]*)" is 1 and "([^"]*)" for ([^ ,]+), ([^ ,]+), ([^ ,]+) rose by (\d+)$`, func(info, attempts, provider, model, outcome string, n int) error {
		return s.buildInfoAndAttemptsRose(info, attempts, provider, model, outcome, float64(n))
	})
	sc.Step(`^"([^"]*)" for ([^ ,]+), ([^ ,]+) rose by (\d+)$`, func(name, tool, outcome string, n int) error {
		return s.toolCallsRose(name, tool, outcome, float64(n))
	})
	sc.Step(`^"([^"]*)" and "([^"]*)" each rose by (\d+)$`, func(a, b string, n int) error { return s.eachRose(a, b, float64(n)) })
	sc.Step(`^"([^"]*)" rose by (\d+)$`, func(name string, n int) error { return s.rose(name, float64(n)) })
	sc.Step(`^"([^"]*)" is present$`, s.isPresent)
	sc.Step(`^the answer contains none of "([^"]*)", "([^"]*)", "([^"]*)"$`, s.containsNoneOf)
}

func TestMetricsExporterFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "metrics-exporter",
		ScenarioInitializer: initializeExporterScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/metrics_exporter.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("metrics exporter feature failed")
	}
}
