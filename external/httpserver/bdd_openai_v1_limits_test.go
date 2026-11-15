//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// openAILimitsBDDState drives features/openai_v1_limits.feature: a coddy HTTP
// server with an openai provider row whose api_base is a stand-in
// limits-capable upstream (another Coddy or a NeuralDeep hub answering GET
// {api_base}/limits), plus a plain openai row without any source.
type openAILimitsBDDState struct {
	home     string
	upstream *httptest.Server
	mgr      *session.Manager
	server   *Server
	ts       *httptest.Server

	mu         sync.Mutex
	limitsUp   bool
	hits       int
	auths      []string
	lastBody   []byte
	lastStatus int
	now        time.Time
}

const openAILimitsBDDKey = "sk-bdd-openai-limits-key-0123456789abcdef"

func (s *openAILimitsBDDState) reset() error {
	var err error
	s.home, err = os.MkdirTemp("", "coddy-oa-limits-bdd-*")
	if err != nil {
		return err
	}
	s.limitsUp, s.hits, s.auths = true, 0, nil
	s.lastBody, s.lastStatus = nil, 0
	s.now = time.Now()
	s.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits++
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		up := s.limitsUp && r.URL.Path == "/v1/limits"
		now := s.now
		s.mu.Unlock()
		if !up {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprint(w, openAILimitsUpstreamPayload(now))
	}))
	return nil
}

func (s *openAILimitsBDDState) close() {
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
	if s.upstream != nil {
		s.upstream.Close()
		s.upstream = nil
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

// openAILimitsUpstreamPayload is what a limits-capable upstream answers on
// GET {api_base}/limits: the NeuralDeep hub's schema-1 shape plus the coddy
// member carrying the provider usage update Coddy serves. The absolute
// times are built from the fake clock so the age correction at delivery
// keeps them in the future.
func openAILimitsUpstreamPayload(now time.Time) string {
	at := func(d time.Duration) string { return now.Add(d).UTC().Format(time.RFC3339) }
	return fmt.Sprintf(`{"schema":1,"observed_at":%q,"tier":"pro",
 "unlimited_volume":false,"bypass":false,"fair_use":true,"options":[],
 "key":{"name":"acc","status":"ok","billing_mode":"wallet"},
 "decision":{"can_request":true,"blockers":[],"retry_after_sec":null},
 "chat":{"session":{"used":450,"limit":15000,"remaining":14550,"reset_in_sec":777,
                    "resets_at":%q,"window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":%q,"window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0},
 "daily_capacity":{"pct_used":0.0,"exhausted":false,"resets_at":%q},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},"blocked_models":[],
 "coddy":{"sessionUpdate":"provider_usage","provider":"upstream-hub","providerType":"neuraldeep",
   "observedAt":%q,"fetchedAt":%q,"plan":"pro","keyName":"acc",
   "windows":[{"id":"session","label":"3h","used":450,"limit":15000,"remaining":14550,"usedPercent":3,
               "resetsAt":%q,"resetInSec":777},
              {"id":"week","label":"week","used":9981,"limit":150000,"remaining":140019,"usedPercent":6.65,
               "resetsAt":%q,"resetInSec":22378},
              {"id":"day","label":"day","usedPercent":0,"resetsAt":%q}],
   "rate":{"used":0,"limit":120,"remaining":120,"resetInSec":58},
   "wallet":{"balanceRub":-1229.244167,"spentRub30d":2000.73518}}}`,
		at(0),
		at(777*time.Second), at(22378*time.Second), at(24*time.Hour),
		at(0), at(0),
		at(777*time.Second), at(22378*time.Second), at(24*time.Hour))
}

// buildServer assembles the manager and the HTTP server with two provider
// rows: "hub", an openai row pointed at the stand-in upstream, and "stub",
// an anthropic row, which has no usage source at all. The provider usage
// clock is fake so a read past the cache TTL can be proven without
// sleeping.
func (s *openAILimitsBDDState) buildServer(limitsUp bool) error {
	s.mu.Lock()
	s.limitsUp = limitsUp
	s.mu.Unlock()
	noAuto := false
	cfg := &config.Config{
		Paths: config.Paths{Home: s.home, CWD: s.home, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{
			{Name: "hub", Type: "openai", APIBase: s.upstream.URL + "/v1", APIKey: openAILimitsBDDKey},
			{Name: "stub", Type: "anthropic", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "hub/model", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "stub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "hub/model"},
	}
	cfg.Rules.AutoDiscover = &noAuto
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		_ = snd.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       acp.ContentBlock{Type: "text", Text: "done"},
		})
		return string(acp.StopReasonEndTurn), nil
	}
	sessions, err := os.MkdirTemp("", "coddy-oa-limits-bdd-sessions-*")
	if err != nil {
		return err
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.New(slog.DiscardHandler), s.home, &session.FileStore{Root: sessions})
	s.mgr.SetProviderUsageClock(func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.now
	}, func(d time.Duration, fn func()) (stop func() bool) {
		t := time.AfterFunc(d, fn)
		return t.Stop
	})
	s.server = New(cfg, s.mgr, slog.New(slog.DiscardHandler), s.home)
	s.ts = httptest.NewServer(s.server.Handler())
	return nil
}

func (s *openAILimitsBDDState) givenServerBackedByStandInLimits() error {
	return s.buildServer(true)
}

func (s *openAILimitsBDDState) givenServerBackedByUpstreamWithoutLimits() error {
	return s.buildServer(false)
}

func (s *openAILimitsBDDState) get(path string) error {
	res, err := http.Get(s.ts.URL + path)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	s.mu.Lock()
	s.lastBody, s.lastStatus = body, res.StatusCode
	s.mu.Unlock()
	return nil
}

func (s *openAILimitsBDDState) whenClientAsksLimits() error {
	return s.get("/v1/limits")
}

func (s *openAILimitsBDDState) whenClientAsksLimitsOfProvider(provider string) error {
	return s.get("/v1/limits?provider=" + provider)
}

func (s *openAILimitsBDDState) whenClientAsksLimitsOfModel(model string) error {
	return s.get("/v1/limits?model=" + model)
}

func (s *openAILimitsBDDState) whenReadUsageOf(provider string) error {
	return s.get("/coddy/providers/" + provider + "/usage")
}

// whenReadUsageAgainPastTTL moves the fake clock past the usage cache TTL
// and asks again: a fetch the second read starts would be counted on the
// stand-in.
func (s *openAILimitsBDDState) whenReadUsageAgainPastTTL(provider string) error {
	s.mu.Lock()
	s.now = s.now.Add(2 * time.Minute)
	s.mu.Unlock()
	return s.get("/coddy/providers/" + provider + "/usage")
}

func (s *openAILimitsBDDState) lastLimits() (map[string]interface{}, error) {
	s.mu.Lock()
	body, status := s.lastBody, s.lastStatus
	s.mu.Unlock()
	if status != http.StatusOK {
		return nil, fmt.Errorf("limits status %d: %s", status, body)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("limits body %s: %w", body, err)
	}
	return out, nil
}

func (s *openAILimitsBDDState) lastUsage() (map[string]interface{}, error) {
	s.mu.Lock()
	body := s.lastBody
	s.mu.Unlock()
	var out struct {
		OK    bool                   `json:"ok"`
		Usage map[string]interface{} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("usage body %s: %w", body, err)
	}
	if !out.OK || out.Usage == nil {
		return nil, fmt.Errorf("usage answer not ok: %s", body)
	}
	return out.Usage, nil
}

func (s *openAILimitsBDDState) thenLimitsSchemaAndPlanAndSession(tier string, pct int) error {
	body, err := s.lastLimits()
	if err != nil {
		return err
	}
	if schema, _ := body["schema"].(float64); int(schema) != 1 {
		return fmt.Errorf("schema = %v, want 1", body["schema"])
	}
	if body["tier"] != tier {
		return fmt.Errorf("tier = %v, want %q", body["tier"], tier)
	}
	chat, _ := body["chat"].(map[string]interface{})
	sess, _ := chat["session"].(map[string]interface{})
	used, _ := sess["used"].(float64)
	limit, _ := sess["limit"].(float64)
	if limit <= 0 || int(used*100/limit+0.5) != pct {
		return fmt.Errorf("chat.session used/limit = %v/%v, want %d%%", used, limit, pct)
	}
	if _, ok := body["decision"].(map[string]interface{}); !ok {
		return fmt.Errorf("limits answer carries no decision: %s", body)
	}
	return nil
}

func (s *openAILimitsBDDState) thenLimitsNamesPlan(tier string) error {
	body, err := s.lastLimits()
	if err != nil {
		return err
	}
	if body["tier"] != tier {
		return fmt.Errorf("tier = %v, want %q", body["tier"], tier)
	}
	return nil
}

func (s *openAILimitsBDDState) thenLimitsCarriesCoddyField() error {
	body, err := s.lastLimits()
	if err != nil {
		return err
	}
	coddy, ok := body["coddy"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("limits answer carries no coddy member: %s", body)
	}
	if coddy["providerType"] != "neuraldeep" {
		return fmt.Errorf("coddy.providerType = %v, want the upstream account's type", coddy["providerType"])
	}
	usage := map[string]interface{}(coddy)
	if _, err := windowPercent(usage, "session"); err != nil {
		return fmt.Errorf("coddy windows: %w", err)
	}
	wallet, _ := coddy["wallet"].(map[string]interface{})
	if wallet == nil {
		return fmt.Errorf("coddy carries no wallet: %v", coddy)
	}
	return nil
}

func (s *openAILimitsBDDState) thenLimitsStatus(status int) error {
	s.mu.Lock()
	got, body := s.lastStatus, s.lastBody
	s.mu.Unlock()
	if got != status {
		return fmt.Errorf("limits status %d, want %d: %s", got, status, body)
	}
	return nil
}

func (s *openAILimitsBDDState) thenAPIAskedWithRowKey() error {
	if err := s.mgr.WaitProviderUsageIdle(3 * time.Second); err != nil {
		return err
	}
	s.mu.Lock()
	auths := append([]string(nil), s.auths...)
	s.mu.Unlock()
	if len(auths) == 0 {
		return fmt.Errorf("the stand-in limits API was never asked")
	}
	for i, a := range auths {
		if a != "Bearer "+openAILimitsBDDKey {
			return fmt.Errorf("request %d Authorization = %q, want the row's key", i+1, a)
		}
	}
	return nil
}

func (s *openAILimitsBDDState) thenAPIAskedOnlyOnce() error {
	if err := s.mgr.WaitProviderUsageIdle(3 * time.Second); err != nil {
		return err
	}
	s.mu.Lock()
	n := s.hits
	s.mu.Unlock()
	if n != 1 {
		return fmt.Errorf("the stand-in limits API was asked %d times, want 1", n)
	}
	return nil
}

func (s *openAILimitsBDDState) thenUsageNamesPlanAndUpstreamType(plan string) error {
	usage, err := s.lastUsage()
	if err != nil {
		return err
	}
	if usage["plan"] != plan {
		return fmt.Errorf("plan = %v, want %q", usage["plan"], plan)
	}
	if usage["providerType"] != "neuraldeep" {
		return fmt.Errorf("providerType = %v, want the upstream account's type", usage["providerType"])
	}
	if usage["provider"] != "hub" {
		return fmt.Errorf("provider = %v, want the local row name", usage["provider"])
	}
	got, err := windowPercent(usage, "session")
	if err != nil {
		return err
	}
	if got != 3 {
		return fmt.Errorf("session window at %d%%, want 3%%", got)
	}
	return nil
}

func (s *openAILimitsBDDState) thenUsageCarriesResetsAndWallet() error {
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

func (s *openAILimitsBDDState) thenAnswerMarksUnsupported() error {
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

func initializeOpenAILimitsScenario(sc *godog.ScenarioContext) {
	s := &openAILimitsBDDState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a coddy HTTP server with an openai provider backed by a stand-in limits API$`, s.givenServerBackedByStandInLimits)
	sc.Step(`^a coddy HTTP server with an openai provider backed by an upstream without a limits endpoint$`, s.givenServerBackedByUpstreamWithoutLimits)
	sc.Step(`^an OpenAI-type client asks for the limits over /v1/limits$`, s.whenClientAsksLimits)
	sc.Step(`^an OpenAI-type client asks for the limits of provider "([^"]*)" over /v1/limits$`, s.whenClientAsksLimitsOfProvider)
	sc.Step(`^an OpenAI-type client asks for the limits of model "([^"]*)" over /v1/limits$`, s.whenClientAsksLimitsOfModel)
	sc.Step(`^I read the usage of the "([^"]*)" provider over REST$`, s.whenReadUsageOf)
	sc.Step(`^the usage of the "([^"]*)" provider is read again after the cache would have expired$`, s.whenReadUsageAgainPastTTL)
	sc.Step(`^the limits answer is schema 1 and names the plan "([^"]*)" with the session window at (\d+)%$`, s.thenLimitsSchemaAndPlanAndSession)
	sc.Step(`^the limits answer is schema 1 and names the plan "([^"]*)"$`, s.thenLimitsNamesPlan)
	sc.Step(`^the limits answer carries the upstream provider usage in its coddy field$`, s.thenLimitsCarriesCoddyField)
	sc.Step(`^the limits answer is a (\d+)$`, s.thenLimitsStatus)
	sc.Step(`^the stand-in limits API was asked with the row's key$`, s.thenAPIAskedWithRowKey)
	sc.Step(`^the stand-in limits API was asked only once$`, s.thenAPIAskedOnlyOnce)
	sc.Step(`^the usage names the plan "([^"]*)" and the provider type of the upstream account$`, s.thenUsageNamesPlanAndUpstreamType)
	sc.Step(`^the usage carries the reset times and the wallet balance of the account$`, s.thenUsageCarriesResetsAndWallet)
	sc.Step(`^the usage answer marks the provider as unsupported$`, s.thenAnswerMarksUnsupported)
}

func TestOpenAILimitsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "openai-v1-limits",
		ScenarioInitializer: initializeOpenAILimitsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/openai_v1_limits.feature"},
			Tags:     "@http",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("openai v1 limits feature suite failed")
	}
}
