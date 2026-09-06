package session

import (
	"context"
	"encoding/json"
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
	if _, _ = m.ProviderUsage(ctx, "neuraldeep", true); stand.calls.Load() != 2 {
		t.Fatalf("inside the backoff no request goes out: calls=%d", stand.calls.Load())
	}
	clock.advance(20 * time.Second)
	stand.set(http.StatusOK, usageFixture(77), nil)
	u, _ = m.ProviderUsage(ctx, "neuraldeep", true)
	if stand.calls.Load() != 3 || u.Stale || u.Error != "" || *findWindow(*u, "session").Used != 77 {
		t.Fatalf("after the backoff: calls=%d update=%+v", stand.calls.Load(), u)
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
