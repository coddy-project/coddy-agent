//go:build http

package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// neuralDeepUsageBDDState drives features/neuraldeep_usage.feature: a coddy
// HTTP server over a stub runner, a neuraldeep provider whose stored hub login
// points at a stand-in GET /limits, and a plain provider without a usage
// source.
type neuralDeepUsageBDDState struct {
	home   string
	api    *httptest.Server
	mgr    *session.Manager
	server *Server
	ts     *httptest.Server

	mu          sync.Mutex
	sessionUsed int
	pendingUsed int
	auths       []string
	lastBody    []byte
	lastStatus  int

	events *eventsSubscriber

	prevBaseEnv string
	prevKeyEnv  string
	prevHubEnv  string
}

const neuralDeepUsageBDDKey = "sk-bdd-usage-key-0123456789abcdef"

func (s *neuralDeepUsageBDDState) reset() error {
	var err error
	s.home, err = os.MkdirTemp("", "coddy-nd-usage-bdd-*")
	if err != nil {
		return err
	}
	s.sessionUsed, s.pendingUsed = 407, -1
	s.auths, s.lastBody, s.lastStatus = nil, nil, 0
	s.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		used := s.sessionUsed
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprint(w, neuralDeepUsagePayload(used))
	}))
	s.prevBaseEnv = os.Getenv(llm.EnvNeuralDeepBaseURL)
	s.prevKeyEnv = os.Getenv("NEURALDEEP_API_KEY")
	s.prevHubEnv = os.Getenv(llm.EnvNeuralDeepHubURL)
	if err := os.Setenv(llm.EnvNeuralDeepBaseURL, s.api.URL); err != nil {
		return err
	}
	if err := os.Setenv(llm.EnvNeuralDeepHubURL, "https://hub.bdd.invalid"); err != nil {
		return err
	}
	return os.Setenv("NEURALDEEP_API_KEY", "")
}

func (s *neuralDeepUsageBDDState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.server != nil {
		s.server.Drain()
		s.server = nil
	}
	if s.mgr != nil {
		s.mgr.ShutdownProviderUsage(2 * time.Second)
		s.mgr = nil
	}
	if s.api != nil {
		s.api.Close()
		s.api = nil
	}
	_ = os.Setenv(llm.EnvNeuralDeepBaseURL, s.prevBaseEnv)
	_ = os.Setenv("NEURALDEEP_API_KEY", s.prevKeyEnv)
	_ = os.Setenv(llm.EnvNeuralDeepHubURL, s.prevHubEnv)
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

// neuralDeepUsagePayload renders the hub's schema-1 answer for a pro wallet
// key with the session counter at used (limit 15000, week 9981 of 150000).
func neuralDeepUsagePayload(used int) string {
	return `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","tier_expires_at":null,
 "unlimited_volume":false,"options":[],"bypass":false,"fair_use":true,
 "key":{"name":"coddy","status":"ok","billing_mode":"wallet","cap":null},
 "decision":{"scope":"chat","can_request":true,"blockers":[],"retry_after_sec":null},
 "chat":{"session":{"used":` + strconv.Itoa(used) + `,"limit":15000,"remaining":` + strconv.Itoa(15000-used) + `,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0,"scope":"account"},
 "vector":{"session":{"used":0,"limit":300000},"rpm_limit":120,"inflight_limit":8},
 "parallel_limit":16,"abuse_cooldown_sec":0,
 "daily_capacity":{"pct_used":0.0,"exhausted":false,"resets_at":"2026-09-07T00:00:00+00:00"},
 "night":{"enabled":true,"active":false,"capacity_factor":2,"window_start_msk":0,"window_end_msk":6},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},"kimi":null}`
}

func (s *neuralDeepUsageBDDState) givenServerWithProviderLoginAndLimits() error {
	return s.buildServer(true)
}

// givenServerWithPanelOff is the same server with the neuraldeep row's usage
// limits panel switched off (providers[].usage_limits_panel: false).
func (s *neuralDeepUsageBDDState) givenServerWithPanelOff() error {
	return s.buildServer(false)
}

// buildServer assembles the manager and the HTTP server; panel says whether
// the neuraldeep row keeps its usage limits panel on.
func (s *neuralDeepUsageBDDState) buildServer(panel bool) error {
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(s.home, "neuraldeep"), neuralDeepUsageBDDKey, "https://hub.bdd.invalid", llm.NeuralDeepClientID, "coddy"); err != nil {
		return err
	}
	noAuto := false
	neuraldeep := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
	if !panel {
		off := false
		neuraldeep.UsageLimitsPanel = &off
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: s.home, CWD: s.home, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{
			neuraldeep,
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "stub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "neuraldeep/qwen3.8-27b"},
	}
	cfg.Rules.AutoDiscover = &noAuto
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		// The turn spends quota on the hub: the stand-in advances its counter
		// when the turn ends, as the real gateway does per request.
		s.mu.Lock()
		if s.pendingUsed >= 0 {
			s.sessionUsed = s.pendingUsed
			s.pendingUsed = -1
		}
		s.mu.Unlock()
		_ = snd.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       acp.ContentBlock{Type: "text", Text: "done"},
		})
		return string(acp.StopReasonEndTurn), nil
	}
	sessions, err := os.MkdirTemp("", "coddy-nd-usage-bdd-sessions-*")
	if err != nil {
		return err
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.New(slog.DiscardHandler), s.home, &session.FileStore{Root: sessions})
	s.server = New(cfg, s.mgr, slog.New(slog.DiscardHandler), s.home)
	s.ts = httptest.NewServer(s.server.Handler())
	return nil
}

func (s *neuralDeepUsageBDDState) readUsage(provider string) (map[string]interface{}, error) {
	res, err := http.Get(s.ts.URL + "/coddy/providers/" + provider + "/usage")
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	s.mu.Lock()
	s.lastBody, s.lastStatus = body, res.StatusCode
	s.mu.Unlock()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage status %d: %s", res.StatusCode, body)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("usage body %s: %w", body, err)
	}
	return out, nil
}

func (s *neuralDeepUsageBDDState) whenReadNeuralDeepUsage() error {
	_, err := s.readUsage("neuraldeep")
	return err
}

func (s *neuralDeepUsageBDDState) whenReadUsageOf(provider string) error {
	_, err := s.readUsage(provider)
	return err
}

func (s *neuralDeepUsageBDDState) lastUsage() (map[string]interface{}, error) {
	s.mu.Lock()
	body := s.lastBody
	s.mu.Unlock()
	var out struct {
		OK    bool                   `json:"ok"`
		Usage map[string]interface{} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK || out.Usage == nil {
		return nil, fmt.Errorf("usage answer not ok: %s", body)
	}
	return out.Usage, nil
}

func usageWindow(usage map[string]interface{}, id string) (map[string]interface{}, bool) {
	windows, _ := usage["windows"].([]interface{})
	for _, w := range windows {
		m, _ := w.(map[string]interface{})
		if m["id"] == id {
			return m, true
		}
	}
	return nil, false
}

func windowPercent(usage map[string]interface{}, id string) (int, error) {
	w, ok := usageWindow(usage, id)
	if !ok {
		return 0, fmt.Errorf("no %s window in %v", id, usage["windows"])
	}
	pct, _ := w["usedPercent"].(float64)
	return int(math.Round(pct)), nil
}

func (s *neuralDeepUsageBDDState) thenUsageNamesPlanAndWindows(plan string, sessionPct, weekPct int) error {
	usage, err := s.lastUsage()
	if err != nil {
		return err
	}
	if usage["plan"] != plan || usage["providerType"] != "neuraldeep" || usage["provider"] != "neuraldeep" {
		return fmt.Errorf("plan/provider = %v/%v/%v", usage["plan"], usage["provider"], usage["providerType"])
	}
	got, err := windowPercent(usage, "session")
	if err != nil {
		return err
	}
	if got != sessionPct {
		return fmt.Errorf("session window at %d%%, want %d%%", got, sessionPct)
	}
	if got, err = windowPercent(usage, "week"); err != nil {
		return err
	} else if got != weekPct {
		return fmt.Errorf("week window at %d%%, want %d%%", got, weekPct)
	}
	return nil
}

func (s *neuralDeepUsageBDDState) thenUsageCarriesResetsAndWallet() error {
	usage, err := s.lastUsage()
	if err != nil {
		return err
	}
	for _, id := range []string{"session", "week"} {
		w, ok := usageWindow(usage, id)
		if !ok {
			return fmt.Errorf("no %s window", id)
		}
		if at, _ := w["resetsAt"].(string); at == "" {
			return fmt.Errorf("%s window has no reset time: %v", id, w)
		}
		if in, _ := w["resetInSec"].(float64); in <= 0 {
			return fmt.Errorf("%s window has no relative reset: %v", id, w)
		}
	}
	wallet, _ := usage["wallet"].(map[string]interface{})
	if balance, _ := wallet["balanceRub"].(float64); balance > -1229 || balance < -1230 {
		return fmt.Errorf("wallet = %v, want the account's ruble balance", usage["wallet"])
	}
	return nil
}

func (s *neuralDeepUsageBDDState) thenAPIAskedWithHubKeyAndAnswerNeverCarriesIt() error {
	s.mu.Lock()
	auths := append([]string(nil), s.auths...)
	body := string(s.lastBody)
	s.mu.Unlock()
	if len(auths) == 0 {
		return fmt.Errorf("the stand-in limits API was never asked")
	}
	for i, a := range auths {
		if a != "Bearer "+neuralDeepUsageBDDKey {
			return fmt.Errorf("request %d Authorization = %q, want the hub key", i+1, a)
		}
	}
	if strings.Contains(body, neuralDeepUsageBDDKey) || strings.Contains(body, "sk-") {
		return fmt.Errorf("the REST answer carries a key: %s", body)
	}
	return nil
}

func (s *neuralDeepUsageBDDState) givenAPIWillReportSessionAfterNextTurn(pct int) error {
	s.mu.Lock()
	s.pendingUsed = pct * 15000 / 100
	s.mu.Unlock()
	return nil
}

// eventsSubscriber reads GET /coddy/events frames into a channel.
type eventsSubscriber struct {
	frames chan string
	cancel context.CancelFunc
}

func (s *neuralDeepUsageBDDState) subscribeEvents() (*eventsSubscriber, error) {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	sub := &eventsSubscriber{frames: make(chan string, 64), cancel: cancel}
	ready := make(chan struct{})
	go func() {
		defer func() { _ = res.Body.Close() }()
		reader := bufio.NewReader(res.Body)
		var frame strings.Builder
		readyOnce := sync.Once{}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\n" {
				text := frame.String()
				frame.Reset()
				if strings.HasPrefix(text, "event: ready") {
					readyOnce.Do(func() { close(ready) })
					continue
				}
				select {
				case sub.frames <- text:
				default:
				}
				continue
			}
			frame.WriteString(line)
		}
	}()
	select {
	case <-ready:
		return sub, nil
	case <-time.After(3 * time.Second):
		cancel()
		return nil, fmt.Errorf("events stream never became ready")
	}
}

func (s *neuralDeepUsageBDDState) whenPromptTurnFinishes() error {
	sub, err := s.subscribeEvents()
	if err != nil {
		return err
	}
	s.events = sub
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.home})
	if err != nil {
		return err
	}
	_, err = s.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "spend some quota"}},
	}, noopSender{}, nil)
	return err
}

func (s *neuralDeepUsageBDDState) thenEventsStreamAnnouncesUsage(pct int) error {
	if s.events == nil {
		return fmt.Errorf("no events subscription")
	}
	defer s.events.cancel()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-s.events.frames:
			if !strings.HasPrefix(frame, "event: provider_usage") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(frame[strings.Index(frame, "data: "):], "data: "))
			var payload struct {
				Object    string                 `json:"object"`
				SessionID string                 `json:"sessionId"`
				Usage     map[string]interface{} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				return fmt.Errorf("provider_usage frame %q: %w", data, err)
			}
			if payload.Object != "coddy.provider_usage" || payload.SessionID == "" || payload.Usage == nil {
				return fmt.Errorf("provider_usage frame = %+v", payload)
			}
			got, err := windowPercent(payload.Usage, "session")
			if err != nil {
				return err
			}
			if got != pct {
				return fmt.Errorf("events announced the session window at %d%%, want %d%%", got, pct)
			}
			return nil
		case <-deadline:
			return fmt.Errorf("no provider_usage event within 5s")
		}
	}
}

func (s *neuralDeepUsageBDDState) thenUsageOverRESTShowsSession(pct int) error {
	usage, err := s.readUsage("neuraldeep")
	if err != nil {
		return err
	}
	inner, _ := usage["usage"].(map[string]interface{})
	got, err := windowPercent(inner, "session")
	if err != nil {
		return err
	}
	if got != pct {
		return fmt.Errorf("REST shows the session window at %d%%, want %d%%", got, pct)
	}
	return nil
}

func (s *neuralDeepUsageBDDState) thenAnswerMarksUnsupported() error {
	s.mu.Lock()
	body, status := s.lastBody, s.lastStatus
	s.mu.Unlock()
	var out struct {
		OK          bool `json:"ok"`
		Unsupported bool `json:"unsupported"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("body %s: %w", body, err)
	}
	if status != http.StatusOK || out.OK || !out.Unsupported {
		return fmt.Errorf("status %d body %s, want ok:false unsupported:true", status, body)
	}
	return nil
}

// thenAnswerMarksUnsupportedBecauseDisabled: a row whose panel is switched
// off answers like a provider without a source and says why.
func (s *neuralDeepUsageBDDState) thenAnswerMarksUnsupportedBecauseDisabled() error {
	s.mu.Lock()
	body, status := s.lastBody, s.lastStatus
	s.mu.Unlock()
	var out struct {
		OK           bool   `json:"ok"`
		Unsupported  bool   `json:"unsupported"`
		Disabled     bool   `json:"disabled"`
		Provider     string `json:"provider"`
		ProviderType string `json:"providerType"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("body %s: %w", body, err)
	}
	if status != http.StatusOK || out.OK || !out.Unsupported || !out.Disabled || out.Provider != "neuraldeep" || out.ProviderType != "neuraldeep" {
		return fmt.Errorf("status %d body %s, want ok:false unsupported:true disabled:true for the neuraldeep row", status, body)
	}
	return nil
}

// thenAPIWasNeverAsked joins the manager's fetches first, so a request still
// in flight would be counted.
func (s *neuralDeepUsageBDDState) thenAPIWasNeverAsked() error {
	if err := s.mgr.WaitProviderUsageIdle(3 * time.Second); err != nil {
		return err
	}
	s.mu.Lock()
	n := len(s.auths)
	s.mu.Unlock()
	if n != 0 {
		return fmt.Errorf("the stand-in limits API was asked %d times, want none", n)
	}
	return nil
}

// thenEventsStreamAnnouncesNoUsage drains the events stream for a moment
// after the fetches are idle: a wrongly published snapshot would already be
// on its way.
func (s *neuralDeepUsageBDDState) thenEventsStreamAnnouncesNoUsage() error {
	if s.events == nil {
		return fmt.Errorf("no events subscription")
	}
	defer s.events.cancel()
	if err := s.mgr.WaitProviderUsageIdle(3 * time.Second); err != nil {
		return err
	}
	quiet := time.After(300 * time.Millisecond)
	for {
		select {
		case frame := <-s.events.frames:
			if strings.HasPrefix(frame, "event: provider_usage") {
				return fmt.Errorf("the events stream announced usage for a row whose panel is switched off: %s", frame)
			}
		case <-quiet:
			return nil
		}
	}
}

func initializeNeuralDeepUsageScenario(sc *godog.ScenarioContext) {
	s := &neuralDeepUsageBDDState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.events != nil {
			s.events.cancel()
			s.events = nil
		}
		s.close()
		return ctx, nil
	})
	sc.Step(`^a coddy HTTP server with a neuraldeep provider, a stored hub login and a stand-in limits API$`, s.givenServerWithProviderLoginAndLimits)
	sc.Step(`^I read the neuraldeep provider usage over REST$`, s.whenReadNeuralDeepUsage)
	sc.Step(`^the usage names the plan "([^"]*)" with the session window at (\d+)% and the week window at (\d+)%$`, s.thenUsageNamesPlanAndWindows)
	sc.Step(`^the usage carries the reset times and the wallet balance of the account$`, s.thenUsageCarriesResetsAndWallet)
	sc.Step(`^the stand-in limits API was asked with the hub key and the answer never carries it$`, s.thenAPIAskedWithHubKeyAndAnswerNeverCarriesIt)
	sc.Step(`^the stand-in limits API will report the session window at (\d+)% after the next turn$`, s.givenAPIWillReportSessionAfterNextTurn)
	sc.Step(`^a prompt turn finishes on the server$`, s.whenPromptTurnFinishes)
	sc.Step(`^the server-wide events stream announces the neuraldeep usage at (\d+)%$`, s.thenEventsStreamAnnouncesUsage)
	sc.Step(`^the neuraldeep provider usage over REST shows the session window at (\d+)%$`, s.thenUsageOverRESTShowsSession)
	sc.Step(`^I read the usage of the "([^"]*)" provider over REST$`, s.whenReadUsageOf)
	sc.Step(`^the usage answer marks the provider as unsupported$`, s.thenAnswerMarksUnsupported)
	sc.Step(`^a coddy HTTP server with a neuraldeep provider whose usage limits panel is switched off, a stored hub login and a stand-in limits API$`, s.givenServerWithPanelOff)
	sc.Step(`^the usage answer marks the provider as unsupported because its usage limits panel is switched off$`, s.thenAnswerMarksUnsupportedBecauseDisabled)
	sc.Step(`^the stand-in limits API was never asked$`, s.thenAPIWasNeverAsked)
	sc.Step(`^the server-wide events stream announces no usage$`, s.thenEventsStreamAnnouncesNoUsage)
}

func TestNeuralDeepUsageFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "neuraldeep-usage",
		ScenarioInitializer: initializeNeuralDeepUsageScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/neuraldeep_usage.feature"},
			Tags:     "@http",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("neuraldeep usage feature suite failed")
	}
}
