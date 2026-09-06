package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// usageCapture records provider usage updates the manager sends.
type usageCapture struct {
	mu      sync.Mutex
	updates []acp.ProviderUsageUpdate
	ids     []string
}

func (c *usageCapture) SendSessionUpdate(id string, update interface{}) error {
	if u, ok := update.(acp.ProviderUsageUpdate); ok {
		c.mu.Lock()
		c.updates = append(c.updates, u)
		c.ids = append(c.ids, id)
		c.mu.Unlock()
	}
	return nil
}

func (*usageCapture) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (*usageCapture) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (c *usageCapture) snapshot() ([]acp.ProviderUsageUpdate, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.ProviderUsageUpdate(nil), c.updates...), append([]string(nil), c.ids...)
}

// usageStand is a stand-in for the hub's GET /v1/limits.
type usageStand struct {
	srv     *httptest.Server
	calls   atomic.Int32
	mu      sync.Mutex
	status  int
	body    string
	headers map[string]string
	auths   []string
	gate    chan struct{}
}

func newUsageStand(t *testing.T) *usageStand {
	t.Helper()
	s := &usageStand{status: http.StatusOK, body: usageFixture(407)}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" {
			http.NotFound(w, r)
			return
		}
		s.calls.Add(1)
		s.mu.Lock()
		status, body, headers := s.status, s.body, s.headers
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.mu.Unlock()
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *usageStand) set(status int, body string, headers map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body, s.headers = status, body, headers
}

func (s *usageStand) lastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.auths) == 0 {
		return ""
	}
	return s.auths[len(s.auths)-1]
}

// usageFixture renders the hub payload with the session counter at used.
func usageFixture(sessionUsed int) string {
	return strings.ReplaceAll(usageFixtureTemplate, "__SESSION_USED__", strconv.Itoa(sessionUsed))
}

const usageFixtureTemplate = `{
  "schema": 1, "observed_at": "2026-09-06T17:47:02Z", "tier": "pro", "tier_expires_at": null,
  "unlimited_volume": false,
  "options": [{"code": "qwen_unlim", "title": "Qwen ∞", "models": ["qwen3.6-35b-a3b"],
               "rpm": {"limit": 60, "used": 0, "remaining": 60, "window": "1m"},
               "inflight_limit": 4, "unlimited_volume": true, "scope": "account"}],
  "bypass": false, "fair_use": true,
  "key": {"name": "coddy", "status": "ok", "billing_mode": "wallet", "cap": null},
  "decision": {"scope": "chat", "can_request": true, "blockers": [], "retry_after_sec": null},
  "chat": {
    "session": {"used": __SESSION_USED__, "limit": 15000, "remaining": 14593, "reset_in_sec": 777,
                "resets_at": "2026-09-06T17:59:59Z", "window": "3h"},
    "week": {"used": 9981, "limit": 150000, "remaining": 140019, "reset_in_sec": 22378,
             "resets_at": "2026-09-07T00:00:00Z", "window": "iso-week"},
    "rpm": {"used": 2, "limit": 120, "remaining": 118, "reset_in_sec": 58},
    "cooldown_sec": 0, "scope": "account"
  },
  "daily_capacity": {"pct_used": 12.5, "exhausted": false, "resets_at": "2026-09-07T00:00:00+00:00"},
  "night": {"enabled": true, "active": false, "capacity_factor": 2},
  "wallet": {"balance_rub": -1229.244167, "spent_rub_30d": 2000.73518},
  "kimi": null
}`

const usageKey = "sk-usage-test-key-0123456789abcdef"

// newUsageManager builds a manager over a neuraldeep provider with a stored
// hub login that points at stand, plus a plain openai provider without a
// usage source.
func newUsageManager(t *testing.T, stand *usageStand, sender acp.UpdateSender, runner AgentRunner) *Manager {
	t.Helper()
	home := t.TempDir()
	t.Setenv(llm.EnvNeuralDeepBaseURL, stand.srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(home, "neuraldeep"), usageKey, "https://hub.example", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: t.TempDir()},
		Providers: []config.ProviderConfig{
			{Name: "neuraldeep", Type: "neuraldeep"},
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "neuraldeep/qwen3.6-35b-a3b", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "stub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "neuraldeep/qwen3.8-27b"},
	}
	noAuto := false
	cfg.Rules.AutoDiscover = &noAuto
	if runner == nil {
		runner = func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
			return string(acp.StopReasonEndTurn), nil
		}
	}
	return NewManager(cfg, sender, runner, slog.New(slog.DiscardHandler), cfg.Paths.CWD, nil)
}

// fakeUsageClock is the injectable clock and timer of the collector.
type fakeUsageClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeUsageTimer
}

type fakeUsageTimer struct {
	at time.Time
	fn func()
}

func newFakeUsageClock() *fakeUsageClock {
	return &fakeUsageClock{now: time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)}
}

func (c *fakeUsageClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeUsageClock) After(d time.Duration, fn func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers = append(c.timers, fakeUsageTimer{at: c.now.Add(d), fn: fn})
	idx := len(c.timers) - 1
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if idx < len(c.timers) && c.timers[idx].fn != nil {
			c.timers[idx].fn = nil
			return true
		}
		return false
	}
}

// advance moves the clock and fires every timer that came due, in order.
func (c *fakeUsageClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []func()
	for i := range c.timers {
		if c.timers[i].fn != nil && !c.timers[i].at.After(c.now) {
			due = append(due, c.timers[i].fn)
			c.timers[i].fn = nil
		}
	}
	c.mu.Unlock()
	for _, fn := range due {
		fn()
	}
}

func (c *fakeUsageClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, tm := range c.timers {
		if tm.fn != nil {
			n++
		}
	}
	return n
}

func decodeUsageFixture(t *testing.T, body string) *llm.NeuralDeepUsage {
	t.Helper()
	var u llm.NeuralDeepUsage
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return &u
}

func findWindow(u acp.ProviderUsageUpdate, id string) *acp.UsageWindow {
	for i := range u.Windows {
		if u.Windows[i].ID == id {
			return &u.Windows[i]
		}
	}
	return nil
}

func TestMapNeuralDeepUsageWindowsRateWalletAndOptions(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)
	u := mapNeuralDeepUsage(decodeUsageFixture(t, usageFixture(407)), "neuraldeep", fetchedAt)
	if u.SessionUpdate != acp.UpdateTypeProviderUsage || u.Provider != "neuraldeep" || u.ProviderType != "neuraldeep" {
		t.Fatalf("header = %+v", u)
	}
	if u.Plan != "pro" || u.KeyName != "coddy" || u.ObservedAt != "2026-09-06T17:47:02Z" || u.FetchedAt != fetchedAt.Format(time.RFC3339) {
		t.Fatalf("plan/key/times = %q %q %q %q", u.Plan, u.KeyName, u.ObservedAt, u.FetchedAt)
	}
	s := findWindow(u, "session")
	if s == nil || s.Label != "3h" || *s.Used != 407 || *s.Limit != 15000 || *s.Remaining != 14593 ||
		s.ResetInSec != 777 || s.ResetsAt != "2026-09-06T17:59:59Z" || s.UsedPercent < 2.7 || s.UsedPercent > 2.72 {
		t.Fatalf("session window = %+v", s)
	}
	w := findWindow(u, "week")
	if w == nil || w.Label != "week" || *w.Used != 9981 || w.ResetInSec != 22378 || w.UsedPercent < 6.65 || w.UsedPercent > 6.66 {
		t.Fatalf("week window = %+v", w)
	}
	d := findWindow(u, "day")
	if d == nil || d.Label != "day" || d.Used != nil || d.Limit != nil || d.UsedPercent != 12.5 || d.Exhausted {
		t.Fatalf("day window = %+v", d)
	}
	// resets_at 2026-09-07T00:00:00Z minus observed_at 17:47:02Z.
	if d.ResetInSec != 22378 || d.ResetsAt == "" {
		t.Fatalf("day reset = %d %q, want derived from resets_at - observed_at", d.ResetInSec, d.ResetsAt)
	}
	if u.Rate == nil || u.Rate.Used != 2 || u.Rate.Limit != 120 || u.Rate.Remaining != 118 || u.Rate.ResetInSec != 58 {
		t.Fatalf("rate = %+v", u.Rate)
	}
	if u.Wallet == nil || u.Wallet.BalanceRub > -1229 || u.Wallet.SpentRub30d < 2000 {
		t.Fatalf("wallet = %+v", u.Wallet)
	}
	if u.Blocked || len(u.Blockers) != 0 || u.RetryAt != "" || u.RetryInSec != 0 || u.Unlimited || u.Stale || u.Error != "" {
		t.Fatalf("state flags = %+v", u)
	}
	if len(u.UnlimitedModels) != 1 || u.UnlimitedModels[0] != "qwen3.6-35b-a3b" {
		t.Fatalf("unlimited models = %v", u.UnlimitedModels)
	}
	if u.CooldownSec != 0 {
		t.Fatalf("cooldown = %d", u.CooldownSec)
	}
}

func TestMapNeuralDeepUsageUnlimitedKeysBlockersAndEdges(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)
	bypass := `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","bypass":true,"fair_use":false,
	 "key":{"name":"primary","status":"ok","billing_mode":"wallet"},
	 "decision":{"scope":"chat","can_request":false,"blockers":["wallet_empty"],"retry_after_sec":null},
	 "chat":{"session":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null,"resets_at":null,"window":"3h"},
	         "week":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null,"resets_at":null,"window":"iso-week"},
	         "rpm":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null},"cooldown_sec":null},
	 "daily_capacity":{"pct_used":140.0,"exhausted":true,"resets_at":"2026-09-07T00:00:00Z"},
	 "wallet":null,"kimi":null}`
	u := mapNeuralDeepUsage(decodeUsageFixture(t, bypass), "neuraldeep", fetchedAt)
	if !u.Unlimited {
		t.Fatalf("bypass key must be unlimited: %+v", u)
	}
	if findWindow(u, "session") != nil || findWindow(u, "week") != nil {
		t.Fatalf("null-limit windows must be dropped: %+v", u.Windows)
	}
	if d := findWindow(u, "day"); d == nil || d.UsedPercent != 100 || !d.Exhausted {
		t.Fatalf("day window must survive on an unlimited key, clamped: %+v", d)
	}
	if u.Rate != nil || u.Wallet != nil {
		t.Fatalf("null rate and wallet must stay nil: %+v %+v", u.Rate, u.Wallet)
	}
	if !u.Blocked || len(u.Blockers) != 1 || u.Blockers[0] != "wallet_empty" || u.RetryAt != "" {
		t.Fatalf("blocked state = %v %v %q", u.Blocked, u.Blockers, u.RetryAt)
	}

	// A timed blocker with a retry delay: RetryAt is observed_at + delay.
	exhausted := strings.Replace(usageFixture(15000),
		`"decision": {"scope": "chat", "can_request": true, "blockers": [], "retry_after_sec": null}`,
		`"decision": {"scope": "chat", "can_request": false, "blockers": ["session_exhausted"], "retry_after_sec": 777}`, 1)
	u = mapNeuralDeepUsage(decodeUsageFixture(t, exhausted), "neuraldeep", fetchedAt)
	if !u.Blocked || u.RetryInSec != 777 || u.RetryAt != "2026-09-06T17:59:59Z" {
		t.Fatalf("timed blocker = blocked %v retryIn %d retryAt %q", u.Blocked, u.RetryInSec, u.RetryAt)
	}
	if s := findWindow(u, "session"); s == nil || s.UsedPercent != 100 || !s.Exhausted {
		t.Fatalf("exhausted session window = %+v", s)
	}

	// A timed blocker without a delay falls back to the window it names.
	noDelay := strings.Replace(usageFixture(15000),
		`"decision": {"scope": "chat", "can_request": true, "blockers": [], "retry_after_sec": null}`,
		`"decision": {"scope": "chat", "can_request": false, "blockers": ["week_exhausted"], "retry_after_sec": null}`, 1)
	u = mapNeuralDeepUsage(decodeUsageFixture(t, noDelay), "neuraldeep", fetchedAt)
	if u.RetryAt != "2026-09-07T00:00:00Z" || u.RetryInSec != 22378 {
		t.Fatalf("fallback retry = %q / %d, want the week window's reset", u.RetryAt, u.RetryInSec)
	}

	// A zero limit keeps the window with a zero percent instead of dividing.
	zero := strings.Replace(usageFixture(0), `"limit": 15000`, `"limit": 0`, 1)
	u = mapNeuralDeepUsage(decodeUsageFixture(t, zero), "neuraldeep", fetchedAt)
	if s := findWindow(u, "session"); s == nil || s.UsedPercent != 0 {
		t.Fatalf("zero-limit window = %+v", s)
	}

	// A cooldown surfaces as CooldownSec.
	cooled := strings.Replace(usageFixture(15000), `"cooldown_sec": 0`, `"cooldown_sec": 725`, 1)
	if u = mapNeuralDeepUsage(decodeUsageFixture(t, cooled), "neuraldeep", fetchedAt); u.CooldownSec != 725 {
		t.Fatalf("cooldown = %d", u.CooldownSec)
	}
}

func TestAgeCorrectedUsageDecrementsRelativeDurations(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)
	base := mapNeuralDeepUsage(decodeUsageFixture(t, usageFixture(407)), "neuraldeep", fetchedAt)
	base.RetryInSec = 30
	later := ageCorrectedUsage(base, fetchedAt.Add(12*time.Second))
	if s := findWindow(later, "session"); s == nil || s.ResetInSec != 765 {
		t.Fatalf("session reset after 12s = %+v, want 765", s)
	}
	if later.Rate == nil || later.Rate.ResetInSec != 46 || later.RetryInSec != 18 {
		t.Fatalf("rate/retry after 12s = %+v / %d", later.Rate, later.RetryInSec)
	}
	if s := findWindow(base, "session"); s.ResetInSec != 777 {
		t.Fatalf("the cached snapshot must not be mutated: %d", s.ResetInSec)
	}
	far := ageCorrectedUsage(base, fetchedAt.Add(time.Hour))
	if s := findWindow(far, "session"); s.ResetInSec != 0 || far.RetryInSec != 0 || far.Rate.ResetInSec != 0 {
		t.Fatalf("durations must clamp at zero: %+v %d", s, far.RetryInSec)
	}
}

func TestProviderUsageCacheTTLFloorAndDeferredRefresh(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	var observed atomic.Int32
	remove := m.AddUsageObserver(func(string, acp.ProviderUsageUpdate) { observed.Add(1) })
	defer remove()
	ctx := context.Background()

	first, err := m.ProviderUsage(ctx, "neuraldeep", false)
	if err != nil || first == nil || first.Plan != "pro" || stand.calls.Load() != 1 {
		t.Fatalf("first read: err=%v update=%+v calls=%d", err, first, stand.calls.Load())
	}
	if stand.lastAuth() != "Bearer "+usageKey {
		t.Fatalf("auth = %q", stand.lastAuth())
	}

	// Inside the TTL an automatic read is served from the cache, age-corrected.
	clock.advance(5 * time.Second)
	stand.set(http.StatusOK, usageFixture(900), nil)
	cached, _ := m.ProviderUsage(ctx, "neuraldeep", false)
	if stand.calls.Load() != 1 || *findWindow(*cached, "session").Used != 407 || findWindow(*cached, "session").ResetInSec != 772 {
		t.Fatalf("cached read: calls=%d window=%+v", stand.calls.Load(), findWindow(*cached, "session"))
	}

	// Inside the floor a refresh is deferred: the cached snapshot comes back now
	// and the fetch fires when the floor expires.
	clock.advance(5 * time.Second) // age 10 s
	deferred, _ := m.ProviderUsage(ctx, "neuraldeep", true)
	if stand.calls.Load() != 1 || *findWindow(*deferred, "session").Used != 407 || clock.pending() != 1 {
		t.Fatalf("deferred refresh: calls=%d used=%d pending=%d", stand.calls.Load(), *findWindow(*deferred, "session").Used, clock.pending())
	}
	clock.advance(6 * time.Second) // age 16 s: the deferred fetch fires
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 2 || observed.Load() == 0 {
		t.Fatalf("deferred fetch: calls=%d observed=%d", stand.calls.Load(), observed.Load())
	}
	fresh, _ := m.ProviderUsage(ctx, "neuraldeep", false)
	if *findWindow(*fresh, "session").Used != 900 {
		t.Fatalf("deferred fetch did not replace the snapshot: %+v", findWindow(*fresh, "session"))
	}

	// Past the floor a refresh runs at once.
	clock.advance(16 * time.Second)
	stand.set(http.StatusOK, usageFixture(1200), nil)
	now, _ := m.ProviderUsage(ctx, "neuraldeep", true)
	if stand.calls.Load() != 3 || *findWindow(*now, "session").Used != 1200 {
		t.Fatalf("immediate refresh: calls=%d used=%d", stand.calls.Load(), *findWindow(*now, "session").Used)
	}

	// Past the TTL an automatic read fetches too.
	clock.advance(21 * time.Second)
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 4 {
		t.Fatalf("expired TTL: calls=%d", stand.calls.Load())
	}
}

func TestProviderUsageStickyUnauthorizedAndDrop(t *testing.T) {
	stand := newUsageStand(t)
	stand.set(http.StatusUnauthorized, `{"detail":"unknown key"}`, nil)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()

	u, err := m.ProviderUsage(ctx, "neuraldeep", false)
	if err != nil || u == nil || u.Error != "unauthorized" || len(u.Windows) != 0 || stand.calls.Load() != 1 {
		t.Fatalf("first: err=%v update=%+v calls=%d", err, u, stand.calls.Load())
	}
	clock.advance(time.Minute)
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 1 {
		t.Fatalf("automatic reads must not retry a rejected key: calls=%d", stand.calls.Load())
	}
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", true); stand.calls.Load() != 2 {
		t.Fatalf("a manual refresh retries: calls=%d", stand.calls.Load())
	}
	clock.advance(time.Minute)
	m.DropProviderUsage("neuraldeep")
	stand.set(http.StatusOK, usageFixture(5), nil)
	u, _ = m.ProviderUsage(ctx, "neuraldeep", false)
	if stand.calls.Load() != 3 || u.Error != "" || findWindow(*u, "session") == nil {
		t.Fatalf("after drop: calls=%d update=%+v", stand.calls.Load(), u)
	}
}

func TestProviderUsageBackoffKeepsStaleWindows(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()

	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	stand.set(http.StatusServiceUnavailable, `{"detail":"counter store unreachable"}`, map[string]string{"Retry-After": "5"})
	u, err := m.ProviderUsage(ctx, "neuraldeep", true)
	if err != nil || u == nil || u.Error != "unavailable" || !u.Stale || findWindow(*u, "session") == nil || stand.calls.Load() != 2 {
		t.Fatalf("stale: err=%v update=%+v calls=%d", err, u, stand.calls.Load())
	}
	clock.advance(2 * time.Second)
	u, _ = m.ProviderUsage(ctx, "neuraldeep", true)
	// The pause ends in 3 s but the floor after the failed attempt lasts 13 s
	// more: the later of the two is when the deferred refresh fires.
	if stand.calls.Load() != 2 || !u.RefreshPending || u.RefreshInSec != 13 {
		t.Fatalf("inside the backoff no request goes out and the answer says when: calls=%d update=%+v", stand.calls.Load(), u)
	}
	stand.set(http.StatusOK, usageFixture(77), nil)
	clock.advance(14 * time.Second)
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 3 {
		t.Fatalf("the deferred refresh did not fire at the floor's end: calls=%d", stand.calls.Load())
	}
	clock.advance(20 * time.Second)
	stand.set(http.StatusOK, usageFixture(78), nil)
	u, _ = m.ProviderUsage(ctx, "neuraldeep", true)
	if stand.calls.Load() != 4 || u.Stale || u.Error != "" || *findWindow(*u, "session").Used != 78 {
		t.Fatalf("after the backoff: calls=%d update=%+v", stand.calls.Load(), u)
	}
}

func TestProviderUsageBackoffIsCappedAndPacingSurvivesAConfigSwap(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	stand.set(http.StatusServiceUnavailable, `{"detail":"later"}`, map[string]string{"Retry-After": "3600"})
	u, _ := m.ProviderUsage(ctx, "neuraldeep", true)
	if u.Error != "unavailable" || stand.calls.Load() != 2 {
		t.Fatalf("503: %+v calls=%d", u, stand.calls.Load())
	}
	clock.advance(4 * time.Minute)
	if u, _ = m.ProviderUsage(ctx, "neuraldeep", true); stand.calls.Load() != 2 || !u.RefreshPending {
		t.Fatalf("inside the capped backoff: calls=%d update=%+v", stand.calls.Load(), u)
	}
	stand.set(http.StatusOK, usageFixture(9), nil)
	clock.advance(2 * time.Minute)
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 3 {
		t.Fatalf("the backoff must end at the cap, not at the hub's hour: calls=%d", stand.calls.Load())
	}
	// A config swap keeps the floor: a refresh right after it is deferred.
	clock.advance(time.Second)
	m.ReplaceConfig(usageConfigClone(m.activeCfg()))
	if u, _ = m.ProviderUsage(ctx, "neuraldeep", true); stand.calls.Load() != 3 || !u.RefreshPending {
		t.Fatalf("after a config swap the floor still holds: calls=%d update=%+v", stand.calls.Load(), u)
	}
}

func TestProviderUsageConfigSwapKeepsADeferredRefresh(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	if _, err := m.ProviderUsageForSession(context.Background(), "s1", "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Second)
	if u, _ := m.ProviderUsageForSession(context.Background(), "s1", "neuraldeep", true); !u.RefreshPending {
		t.Fatalf("expected a deferred refresh: %+v", u)
	}
	// A settings save in between: the promise survives it.
	m.ReplaceConfig(usageConfigClone(m.activeCfg()))
	stand.set(http.StatusOK, usageFixture(321), nil)
	clock.advance(11 * time.Second)
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != "s1" || *findWindow(updates[0], "session").Used != 321 {
		t.Fatalf("deferred refresh after a config swap: updates=%+v ids=%v", updates, ids)
	}
}

func TestProviderUsageTurnsAfterARejectedKeyMakeNoRequest(t *testing.T) {
	stand := newUsageStand(t)
	stand.set(http.StatusUnauthorized, `{"detail":"unknown key"}`, nil)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	id := newUsageSession(t, m, "")
	for i := 0; i < 3; i++ {
		if err := usagePrompt(t, m, id, sender, nil); err != nil {
			t.Fatal(err)
		}
		if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
			t.Fatal(err)
		}
		clock.advance(30 * time.Second)
	}
	updates, _ := sender.snapshot()
	if stand.calls.Load() != 1 || len(updates) != 3 {
		t.Fatalf("calls=%d updates=%d, want one request and a delivery per turn", stand.calls.Load(), len(updates))
	}
	for _, u := range updates {
		if u.Error != "unauthorized" {
			t.Fatalf("turn delivery = %+v, want the sticky rejection", u)
		}
	}
}

func TestProviderUsageCommandBackedRowRetriesARejectionAfterAWhile(t *testing.T) {
	stand := newUsageStand(t)
	stand.set(http.StatusUnauthorized, `{"detail":"unknown key"}`, nil)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	cfg := m.activeCfg()
	cfg.Providers[0].APIKeyCommand = "echo sk-from-helper-0123456789abcdef"
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if u, _ := m.ProviderUsage(ctx, "neuraldeep", false); u.Error != "unauthorized" || stand.calls.Load() != 1 {
		t.Fatalf("first: %+v calls=%d", u, stand.calls.Load())
	}
	clock.advance(30 * time.Second)
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 1 {
		t.Fatalf("inside the retry window the rejection sticks: calls=%d", stand.calls.Load())
	}
	stand.set(http.StatusOK, usageFixture(11), nil)
	clock.advance(time.Minute)
	if u, _ := m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 2 || u.Error != "" {
		t.Fatalf("after the retry window the helper is asked again: calls=%d update=%+v", stand.calls.Load(), u)
	}
}

func TestProviderUsageConfigSwapDuringAFetchAnswersTheWaiterAfterTheFloor(t *testing.T) {
	stand, gate := gatedUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	id := newUsageSession(t, m, "")
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageCalls(t, stand, 1)
	// The settings save lands while the turn-end fetch is blocked upstream.
	// The request may have reached the hub, so it counts as an attempt: the
	// replacement waits for the floor instead of doubling the read.
	m.ReplaceConfig(usageConfigClone(m.activeCfg()))
	close(gate)
	clock.advance(time.Second)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, _ := sender.snapshot(); stand.calls.Load() != 1 || len(updates) != 0 {
		t.Fatalf("inside the floor after the swap: calls=%d updates=%d", stand.calls.Load(), len(updates))
	}
	clock.advance(14 * time.Second)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if stand.calls.Load() != 2 || len(updates) != 1 || ids[0] != id || updates[0].Plan != "pro" {
		t.Fatalf("waiter after a config swap: calls=%d, %d updates to %v", stand.calls.Load(), len(updates), ids)
	}
}

func TestProviderUsageCredentialChangeDuringADeferredRefreshIsolatesAccounts(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Second)
	if u, _ := m.ProviderUsageForSession(ctx, "s1", "neuraldeep", true); !u.RefreshPending {
		t.Fatalf("expected a deferred refresh: %+v", u)
	}
	// The operator pastes another key while the refresh waits: the fetch
	// must describe the new account and reach the session that asked.
	first := m.activeCfg()
	m.ReplaceConfig(usageConfigWithKey(first, "sk-other-account"))
	stand.set(http.StatusOK, usageFixture(999), nil)
	clock.advance(11 * time.Second)
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 2 || stand.lastAuth() != "Bearer sk-other-account" {
		t.Fatalf("the deferred refresh must ask for the new account: calls=%d auth=%q", stand.calls.Load(), stand.lastAuth())
	}
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != "s1" || *findWindow(updates[0], "session").Used != 999 {
		t.Fatalf("s1 was promised the refresh: %+v %v", updates, ids)
	}
	// Back to the first key: its numbers come from the hub, never from the
	// other account's fetch.
	m.ReplaceConfig(first)
	stand.set(http.StatusOK, usageFixture(407), nil)
	clock.advance(time.Second)
	u, err := m.ProviderUsage(ctx, "neuraldeep", false)
	if err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 3 || *findWindow(*u, "session").Used != 407 {
		t.Fatalf("the first account must be read afresh: calls=%d used=%d", stand.calls.Load(), *findWindow(*u, "session").Used)
	}
}

// waitUsageCalls blocks until the stand saw n requests or two seconds passed.
func waitUsageCalls(t *testing.T, stand *usageStand, n int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for stand.calls.Load() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if stand.calls.Load() < n {
		t.Fatalf("the hub saw %d requests, want %d", stand.calls.Load(), n)
	}
}

// usageConfigClone copies cfg with its own provider slice, so a test can
// hand ReplaceConfig a distinct value with or without a changed row.
func usageConfigClone(cfg *config.Config) *config.Config {
	next := *cfg
	next.Providers = append([]config.ProviderConfig(nil), cfg.Providers...)
	return &next
}

// usageConfigWithKey clones cfg with a literal key on the neuraldeep row: a
// different account behind the same provider name.
func usageConfigWithKey(cfg *config.Config, key string) *config.Config {
	next := usageConfigClone(cfg)
	for i := range next.Providers {
		if next.Providers[i].Name == "neuraldeep" {
			next.Providers[i].APIKey = key
		}
	}
	return next
}

func TestProviderUsageLiteralKeyKeepsARejectionStickyDespiteACommand(t *testing.T) {
	stand := newUsageStand(t)
	stand.set(http.StatusUnauthorized, `{"detail":"unknown key"}`, nil)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	cfg := m.activeCfg()
	cfg.Providers[0].APIKey = "sk-literal-key-0123456789abcdef0"
	cfg.Providers[0].APIKeyCommand = "echo sk-unused-helper-0123456789ab"
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if u, _ := m.ProviderUsage(ctx, "neuraldeep", false); u.Error != "unauthorized" || stand.calls.Load() != 1 {
		t.Fatalf("first: %+v calls=%d", u, stand.calls.Load())
	}
	clock.advance(5 * time.Minute)
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 1 {
		t.Fatalf("the literal key outranks the command: the rejection must stay sticky, calls=%d", stand.calls.Load())
	}
}

func TestProviderUsageFingerprintRotation(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil || stand.calls.Load() != 1 {
		t.Fatalf("first: %v calls=%d", err, stand.calls.Load())
	}
	const rotated = "sk-rotated-key-0123456789abcdefg"
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(m.activeCfg().Paths.Home, "neuraldeep"), rotated, "https://hub.example", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Second)
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", false); stand.calls.Load() != 2 || stand.lastAuth() != "Bearer "+rotated {
		t.Fatalf("rotation: calls=%d auth=%q", stand.calls.Load(), stand.lastAuth())
	}
}

func TestProviderUsageUnsupportedAndUnknownProvider(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	u, err := m.ProviderUsage(context.Background(), "stub", false)
	if err != nil || u == nil || !u.Unsupported || u.Provider != "stub" || u.ProviderType != "openai" {
		t.Fatalf("stub provider: err=%v update=%+v", err, u)
	}
	if _, err := m.ProviderUsage(context.Background(), "nope", false); err == nil {
		t.Fatalf("unknown provider must error")
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("no request must go out: %d", stand.calls.Load())
	}
}

func newUsageSession(t *testing.T, m *Manager, model string) string {
	t.Helper()
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: m.defaultCWD})
	if err != nil {
		t.Fatal(err)
	}
	if model != "" {
		if _, err := m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: res.SessionID, ConfigID: "model", Value: model}); err != nil {
			t.Fatal(err)
		}
	}
	return res.SessionID
}

func usagePrompt(t *testing.T, m *Manager, id string, sender acp.UpdateSender, opts *PromptRunOpts) error {
	t.Helper()
	_, err := m.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: id, Prompt: []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}, sender, opts)
	return err
}

func TestProviderUsagePublishedWhenATurnEnds(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	var observedIDs []string
	var obsMu sync.Mutex
	remove := m.AddUsageObserver(func(id string, _ acp.ProviderUsageUpdate) {
		obsMu.Lock()
		observedIDs = append(observedIDs, id)
		obsMu.Unlock()
	})
	defer remove()

	id := newUsageSession(t, m, "")
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != id || updates[0].Plan != "pro" || stand.calls.Load() != 1 {
		t.Fatalf("turn end: updates=%+v ids=%v calls=%d", updates, ids, stand.calls.Load())
	}
	obsMu.Lock()
	seen := append([]string(nil), observedIDs...)
	obsMu.Unlock()
	if len(seen) != 1 || seen[0] != id {
		t.Fatalf("observer ids = %v", seen)
	}
}

func TestProviderUsagePublishedWhenATurnFails(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	failing := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", context.DeadlineExceeded
	}
	m := newUsageManager(t, stand, sender, failing)
	id := newUsageSession(t, m, "")
	if err := usagePrompt(t, m, id, sender, nil); err == nil {
		t.Fatalf("the failing runner must surface its error")
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, _ := sender.snapshot(); len(updates) != 1 {
		t.Fatalf("a failed turn must still refresh the usage: %+v", updates)
	}
}

func TestProviderUsageSkippedForOptedOutSubagentAndUnmeteredTurns(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	id := newUsageSession(t, m, "")

	if err := usagePrompt(t, m, id, sender, &PromptRunOpts{SkipUsagePublish: true}); err != nil {
		t.Fatal(err)
	}
	if err := usagePrompt(t, m, id, sender, &PromptRunOpts{subagentTurn: true}); err != nil {
		t.Fatal(err)
	}
	other := newUsageSession(t, m, "stub/model")
	if err := usagePrompt(t, m, other, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, _ := sender.snapshot(); len(updates) != 0 || stand.calls.Load() != 0 {
		t.Fatalf("no publish expected: updates=%+v calls=%d", updates, stand.calls.Load())
	}
}

func TestProviderUsageTwoSessionsShareOneSnapshot(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	a := newUsageSession(t, m, "neuraldeep/qwen3.8-27b")
	b := newUsageSession(t, m, "neuraldeep/qwen3.6-35b-a3b")
	if err := usagePrompt(t, m, a, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Second)
	if err := usagePrompt(t, m, b, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if len(updates) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("updates=%d ids=%v", len(updates), ids)
	}
	// The second turn ended inside the floor: it is served the same snapshot
	// now and a deferred refresh is pending; the snapshot itself carries no
	// per-session field, only the list each client compares its model with.
	if stand.calls.Load() != 1 || clock.pending() != 1 {
		t.Fatalf("calls=%d pending=%d, want one fetch and one deferred refresh", stand.calls.Load(), clock.pending())
	}
	for _, u := range updates {
		if len(u.UnlimitedModels) != 1 || u.UnlimitedModels[0] != "qwen3.6-35b-a3b" {
			t.Fatalf("snapshot = %+v", u)
		}
	}
}

// usageNoopSender stands for coddy http's manager-wide sender: it drops
// everything, so only the observers can carry a snapshot.
type usageNoopSender struct{}

func (usageNoopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (usageNoopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (usageNoopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// gatedUsageStand is a stand-in whose answer waits for the test to release
// it, so several sessions can join one running fetch. The gate can be
// replaced between fetches (setGate) to hold a later one.
func gatedUsageStand(t *testing.T) (*usageStand, chan struct{}) {
	t.Helper()
	gate := make(chan struct{})
	s := &usageStand{status: http.StatusOK, body: usageFixture(407), gate: gate}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.mu.Lock()
		g := s.gate
		s.mu.Unlock()
		<-g
		s.mu.Lock()
		body := s.body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s, gate
}

func (s *usageStand) setGate(g chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = g
}

func TestProviderUsageFansOutToEverySessionThatJoinedTheFetch(t *testing.T) {
	stand, gate := gatedUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	var observed []string
	var obsMu sync.Mutex
	remove := m.AddUsageObserver(func(id string, _ acp.ProviderUsageUpdate) {
		obsMu.Lock()
		observed = append(observed, id)
		obsMu.Unlock()
	})
	defer remove()

	a := newUsageSession(t, m, "")
	b := newUsageSession(t, m, "neuraldeep/qwen3.6-35b-a3b")
	// Both turns end while the upstream is still answering the first fetch.
	if err := usagePrompt(t, m, a, sender, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for stand.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := usagePrompt(t, m, b, sender, nil); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one coalesced fetch", stand.calls.Load())
	}
	got := map[string]int{}
	for _, id := range ids {
		got[id]++
	}
	if got[a] != 1 || got[b] != 1 || len(updates) != 2 {
		t.Fatalf("deliveries = %v (%d updates), want one fresh snapshot per session", got, len(updates))
	}
	for _, u := range updates {
		if u.Plan != "pro" || findWindow(u, "session") == nil {
			t.Fatalf("delivered snapshot = %+v", u)
		}
	}
	obsMu.Lock()
	seen := map[string]int{}
	for _, id := range observed {
		seen[id]++
	}
	obsMu.Unlock()
	if seen[a] != 1 || seen[b] != 1 {
		t.Fatalf("observers saw %v, want one snapshot per session", seen)
	}
}

func TestProviderUsageDeferredRefreshReachesEveryDeferredSession(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	var observed []string
	var obsMu sync.Mutex
	remove := m.AddUsageObserver(func(id string, _ acp.ProviderUsageUpdate) {
		obsMu.Lock()
		observed = append(observed, id)
		obsMu.Unlock()
	})
	defer remove()
	a := newUsageSession(t, m, "")
	b := newUsageSession(t, m, "neuraldeep/qwen3.6-35b-a3b")
	if err := usagePrompt(t, m, a, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	// Both sessions end a turn inside the floor: both are deferred.
	clock.advance(time.Second)
	if err := usagePrompt(t, m, a, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := usagePrompt(t, m, b, sender, nil); err != nil {
		t.Fatal(err)
	}
	stand.set(http.StatusOK, usageFixture(900), nil)
	clock.advance(15 * time.Second)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	fresh := map[string]int{}
	for i, u := range updates {
		if !u.RefreshPending && findWindow(u, "session") != nil && *findWindow(u, "session").Used == 900 {
			fresh[ids[i]]++
		}
	}
	if fresh[a] != 1 || fresh[b] != 1 {
		t.Fatalf("fresh deliveries = %v (all: %v)", fresh, ids)
	}
	obsMu.Lock()
	seen := map[string]int{}
	for _, id := range observed {
		seen[id]++
	}
	obsMu.Unlock()
	if seen[b] == 0 || seen[a] == 0 {
		t.Fatalf("observers saw %v, want both deferred sessions", seen)
	}
}

func TestProviderUsageRevokedKeyOutranksTheOldNumbers(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	stand.set(http.StatusUnauthorized, `{"detail":"unknown key"}`, nil)
	u, _ := m.ProviderUsage(ctx, "neuraldeep", true)
	if u.Error != "unauthorized" || len(u.Windows) != 0 || u.Wallet != nil || u.Stale || u.Blocked {
		t.Fatalf("after a 401 the old numbers must go: %+v", u)
	}
	clock.advance(30 * time.Second)
	stand.set(http.StatusOK, usageFixture(15000), nil)
	if _, err := m.ProviderUsage(ctx, "neuraldeep", true); err != nil {
		t.Fatal(err)
	}
	// A blocked account replaces whatever blockers the last snapshot carried.
	clock.advance(30 * time.Second)
	stand.set(http.StatusForbidden, `{"detail":"user blocked"}`, nil)
	u, _ = m.ProviderUsage(ctx, "neuraldeep", true)
	if !u.Blocked || len(u.Blockers) != 1 || u.Blockers[0] != "user_blocked" || u.Error != "" || !u.Stale || findWindow(*u, "session") == nil {
		t.Fatalf("after a 403: %+v", u)
	}
}

func TestProviderUsagePublishesFromAnAdmittedTurnThatMarkedItself(t *testing.T) {
	// The door the HTTP permission resume uses: BeginTurn, MarkTurnRan on the
	// turn context, finish.
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	id := newUsageSession(t, m, "")
	turnCtx, finish, err := m.BeginTurn(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	MarkTurnRan(turnCtx)
	finish()
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, ids := sender.snapshot(); len(updates) != 1 || ids[0] != id {
		t.Fatalf("admitted turn published %d updates to %v", len(updates), ids)
	}
	// An admission that never reached its runner publishes nothing.
	_, finish, err = m.BeginTurn(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	finish()
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, _ := sender.snapshot(); len(updates) != 1 {
		t.Fatalf("an unmarked admission published: %d updates", len(updates))
	}
}

func TestProviderUsageReadDuringADeferredFetchJoinsIt(t *testing.T) {
	stand, gate := gatedUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	// A warm cache from a first read, then a refresh inside the floor.
	close(gate)
	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	gate2 := make(chan struct{})
	stand.setGate(gate2)
	stand.set(http.StatusOK, usageFixture(4242), nil)
	clock.advance(5 * time.Second)
	u, _ := m.ProviderUsage(ctx, "neuraldeep", true)
	if !u.RefreshPending {
		t.Fatalf("refresh inside the floor must be deferred: %+v", u)
	}
	// The deferred fetch fires and blocks on the stand-in; a cache read timed
	// on refreshInSec lands during it and must not see the old snapshot.
	clock.advance(11 * time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for stand.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	joined := make(chan *acp.ProviderUsageUpdate, 1)
	go func() {
		got, _ := m.ProviderUsage(ctx, "neuraldeep", false)
		joined <- got
	}()
	select {
	case got := <-joined:
		t.Fatalf("the read answered before the fetch landed: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
	close(gate2)
	select {
	case got := <-joined:
		if got == nil || *findWindow(*got, "session").Used != 4242 || got.RefreshPending {
			t.Fatalf("joined read = %+v, want the deferred fetch's result", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the joined read never returned")
	}
}

func TestProviderUsageReadIsSupersededByACredentialChange(t *testing.T) {
	stand, gate := gatedUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	ctx := context.Background()
	answer := make(chan error, 1)
	go func() {
		_, err := m.ProviderUsage(ctx, "neuraldeep", false)
		answer <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for stand.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// The key rotates while the read waits: whatever the fetch brings back
	// belongs to the old account.
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(m.activeCfg().Paths.Home, "neuraldeep"), "sk-rotated-mid-flight-0123456789", "https://hub.example", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	fresh := make(chan error, 1)
	go func() {
		_, err := m.ProviderUsage(context.Background(), "neuraldeep", false)
		fresh <- err
	}()
	close(gate)
	select {
	case err := <-fresh:
		if err != nil {
			t.Fatalf("the read on the rotated account failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read on the rotated account never returned")
	}
	select {
	case err := <-answer:
		if !errors.Is(err, errUsageSuperseded) {
			t.Fatalf("the superseded read answered %v, want errUsageSuperseded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the superseded read never returned")
	}
}

func TestProviderUsageReadyWaitsForASlowFetch(t *testing.T) {
	stand, gate := gatedUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	id := newUsageSession(t, m, "")
	m.HandleSessionReady(id)
	time.Sleep(100 * time.Millisecond)
	if updates, _ := sender.snapshot(); len(updates) != 0 {
		t.Fatalf("ready delivered before the fetch landed: %+v", updates)
	}
	close(gate)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, ids := sender.snapshot(); len(updates) != 1 || ids[0] != id || updates[0].Plan != "pro" {
		t.Fatalf("ready after a slow fetch: %d updates to %v", len(updates), ids)
	}
}

func TestProviderUsageReadyDeliversExactlyOnce(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	id := newUsageSession(t, m, "")
	m.HandleSessionReady(id)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != id || stand.calls.Load() != 1 {
		t.Fatalf("ready delivered %d updates to %v with %d fetches, want exactly one", len(updates), ids, stand.calls.Load())
	}
	// A second ready inside the TTL answers from the cache, once again.
	m.HandleSessionReady(id)
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if updates, _ = sender.snapshot(); len(updates) != 2 || stand.calls.Load() != 1 {
		t.Fatalf("second ready: %d updates, %d fetches", len(updates), stand.calls.Load())
	}
}

func TestProviderUsageDeferredTurnEndReachesObservers(t *testing.T) {
	stand := newUsageStand(t)
	// A no-op manager sender stands for coddy http, where only the
	// observers listen.
	m := newUsageManager(t, stand, usageNoopSender{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	var pending []bool
	var obsMu sync.Mutex
	remove := m.AddUsageObserver(func(_ string, u acp.ProviderUsageUpdate) {
		obsMu.Lock()
		pending = append(pending, u.RefreshPending)
		obsMu.Unlock()
	})
	defer remove()
	id := newUsageSession(t, m, "")
	if err := usagePrompt(t, m, id, usageNoopSender{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Second)
	if err := usagePrompt(t, m, id, usageNoopSender{}, nil); err != nil {
		t.Fatal(err)
	}
	obsMu.Lock()
	got := append([]bool(nil), pending...)
	obsMu.Unlock()
	if len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("observer saw %v, want a fresh snapshot then a deferred one", got)
	}
}

func TestProviderUsageForSessionReportsADeferredReadToTheSession(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()
	if _, err := m.ProviderUsageForSession(ctx, "s1", "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	if updates, _ := sender.snapshot(); len(updates) != 0 || stand.calls.Load() != 1 {
		t.Fatalf("a read the caller awaits delivers nothing by itself: %d updates, %d calls", len(updates), stand.calls.Load())
	}
	clock.advance(5 * time.Second)
	stand.set(http.StatusOK, usageFixture(555), nil)
	u, err := m.ProviderUsageForSession(ctx, "s1", "neuraldeep", true)
	if err != nil || !u.RefreshPending || u.RefreshInSec == 0 {
		t.Fatalf("inside the floor: err=%v update=%+v", err, u)
	}
	clock.advance(11 * time.Second)
	if err := m.WaitProviderUsageIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != "s1" || *findWindow(updates[0], "session").Used != 555 || updates[0].RefreshPending {
		t.Fatalf("deferred read: updates=%+v ids=%v", updates, ids)
	}
	// The settled snapshot re-arms nothing on the server side: no pending refresh.
	if clock.pending() != 0 {
		t.Fatalf("pending timers = %d", clock.pending())
	}
}
