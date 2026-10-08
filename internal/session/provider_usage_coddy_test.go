package session

// The coddy usage source: the account usage behind a model a remote Coddy
// shares, read per alias through GET /coddy/llm/models/{alias}/usage. The
// manager keeps one entry per (provider, alias) (docs/plans/
// remote-model-provider-phase2.md, 4.3 and 4.4), remembers "unsupported" for
// 60 s, drops the numbers of a vanished alias, and reads once more a TTL after
// the end of a turn.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const coddyUsageToken = "sk-remote-shared-token-0123456789"

type coddyUsageAnswer struct {
	status int
	body   string
	header map[string]string
}

// coddyUsageRemote is a stand-in for the usage route of a remote Coddy: it
// answers per alias with a scripted answer and counts the reads.
type coddyUsageRemote struct {
	srv      *httptest.Server
	mu       sync.Mutex
	answers  map[string]coddyUsageAnswer
	fallback coddyUsageAnswer
	hits     map[string]int
	auths    []string
	other    int
}

func newCoddyUsageRemote(t *testing.T) *coddyUsageRemote {
	t.Helper()
	r := &coddyUsageRemote{
		answers:  map[string]coddyUsageAnswer{},
		fallback: coddyUsageAnswer{status: http.StatusOK, body: coddyUsageDocument(10, 600)},
		hits:     map[string]int{},
	}
	const prefix = "/coddy/llm/models/"
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.EscapedPath()
		if req.Method != http.MethodGet || !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, "/usage") {
			r.mu.Lock()
			r.other++
			r.mu.Unlock()
			http.NotFound(w, req)
			return
		}
		alias, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/usage"))
		r.mu.Lock()
		r.hits[alias]++
		r.auths = append(r.auths, req.Header.Get("Authorization"))
		ans, ok := r.answers[alias]
		if !ok {
			ans = r.fallback
		}
		r.mu.Unlock()
		if ans.header["Content-Type"] == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		for k, v := range ans.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(ans.status)
		_, _ = w.Write([]byte(ans.body))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *coddyUsageRemote) set(alias string, status int, body string, header map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers[alias] = coddyUsageAnswer{status: status, body: body, header: header}
}

func (r *coddyUsageRemote) reads(alias string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[alias]
}

func (r *coddyUsageRemote) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.hits {
		n += v
	}
	return n
}

func (r *coddyUsageRemote) strayRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.other
}

func (r *coddyUsageRemote) lastAuth() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.auths) == 0 {
		return "<no request>"
	}
	return r.auths[len(r.auths)-1]
}

// coddyUsageDocument is the projection a remote answers with: a session
// window at pct percent resetting in resetIn seconds, a week window with no
// reset clock.
func coddyUsageDocument(pct, resetIn int) string {
	return `{"supported":true,"account_wide":true,"stale":false,"windows":[` +
		`{"id":"session","label":"3h","used_percent":` + strconv.Itoa(pct) + `,"reset_in_s":` + strconv.Itoa(resetIn) + `,"exhausted":false},` +
		`{"id":"week","label":"week","used_percent":7.5,"exhausted":false}],"blocked":false}`
}

const (
	coddyUsageUnsupportedBody = `{"supported":false}`
	coddyUsageUnknownModel    = `{"status":404,"kind":"invalid","code":"unknown_model","message":"not shared","emitted":false}`
	coddyUsageNotFound        = `{"status":404,"kind":"invalid","code":"not_found","message":"reserved","emitted":false}`
	coddyUsageHopError        = `{"error":{"node":"n1","message":"no such node"}}`
	coddyUsageHTML            = `<!doctype html><html><body>not found</body></html>`
)

// newCoddyUsageManager builds a manager over one coddy row, "remote", that
// shares the aliases coder and helper, next to a plain openai row without a
// usage source.
func newCoddyUsageManager(t *testing.T, remote *coddyUsageRemote, sender acp.UpdateSender, runner AgentRunner, edit func(*config.Config)) *Manager {
	t.Helper()
	t.Setenv("REMOTE_API_KEY", "")
	home := t.TempDir()
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: t.TempDir()},
		Providers: []config.ProviderConfig{
			{Name: "remote", Type: "coddy", APIBase: remote.srv.URL, APIKey: coddyUsageToken, Proxy: "none"},
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "remote/coder", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "remote/helper", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "stub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "remote/coder"},
	}
	noAuto := false
	cfg.Rules.AutoDiscover = &noAuto
	if runner == nil {
		runner = func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
			return string(acp.StopReasonEndTurn), nil
		}
	}
	if edit != nil {
		edit(cfg)
	}
	m := NewManager(cfg, sender, runner, slog.New(slog.DiscardHandler), cfg.Paths.CWD, nil)
	// The listing of the remote is not what these tests are about.
	m.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) { return nil, nil }, nil)
	t.Cleanup(func() { m.ShutdownProviderUsage(3 * time.Second) })
	return m
}

// newCoddyUsageClockedManager is newCoddyUsageManager with the fake clock
// installed.
func newCoddyUsageClockedManager(t *testing.T, remote *coddyUsageRemote, sender acp.UpdateSender) (*Manager, *fakeUsageClock) {
	t.Helper()
	m := newCoddyUsageManager(t, remote, sender, nil, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	return m, clock
}

// coddyUsageConfig clones cfg, rows included, and lets edit change the clone.
func coddyUsageConfig(cfg *config.Config, edit func(*config.Config)) *config.Config {
	next := *cfg
	next.Providers = append([]config.ProviderConfig(nil), cfg.Providers...)
	next.Models = append([]config.ModelEntry(nil), cfg.Models...)
	if edit != nil {
		edit(&next)
	}
	return &next
}

func usageEntryKeys(m *Manager) []string {
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	keys := make([]string, 0, len(m.usage.entries))
	for k := range m.usage.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustUsage(t *testing.T, m *Manager, name string, refresh bool) acp.ProviderUsageUpdate {
	t.Helper()
	u, err := m.ProviderUsage(context.Background(), name, refresh)
	if err != nil || u == nil {
		t.Fatalf("ProviderUsage(%q, refresh=%v): err=%v update=%+v", name, refresh, err, u)
	}
	return *u
}

func waitUsageIdle(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
}

// --- the mapping -----------------------------------------------------------

func TestMapCoddyUsageProjectsTheWireDocumentAndInventsNothing(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	reset, passed := 777, 0
	w := &llm.WireUsage{
		Supported: true, AccountWide: true, Stale: true,
		Windows: []llm.WireUsageWindow{
			{ID: "session", Label: "3h", UsedPercent: 62, ResetInS: &reset},
			{ID: "week", Label: "week", UsedPercent: 140, Exhausted: true},
			{ID: "day", Label: "day", UsedPercent: -3, ResetInS: &passed},
		},
		Blocked: true, Blockers: []string{"session_exhausted", "model_blocked"}, RetryInS: 120,
	}
	u := mapCoddyUsage(w, "remote", "coder", at)

	if u.SessionUpdate != acp.UpdateTypeProviderUsage || u.Provider != "remote" || u.ProviderType != "coddy" || u.Model != "coder" {
		t.Fatalf("header = %+v", u)
	}
	if u.FetchedAt != "2026-10-08T12:00:00Z" || !u.Stale || u.Unsupported || u.Error != "" {
		t.Fatalf("times and flags = %+v", u)
	}
	if len(u.Windows) != 3 {
		t.Fatalf("windows = %+v", u.Windows)
	}
	s := u.Windows[0]
	if s.ID != "session" || s.Label != "3h" || s.UsedPercent != 62 || s.ResetInSec != 777 || s.ResetsAt != "2026-10-08T12:12:57Z" || s.Used != nil || s.Limit != nil || s.Remaining != nil {
		t.Fatalf("session window = %+v", s)
	}
	if wk := u.Windows[1]; wk.UsedPercent != 100 || !wk.Exhausted || wk.ResetsAt != "" || wk.ResetInSec != 0 {
		t.Fatalf("a window without a reset clock must carry no reset time: %+v", wk)
	}
	if d := u.Windows[2]; d.UsedPercent != 0 || d.ResetInSec != 0 || d.ResetsAt != "2026-10-08T12:00:00Z" {
		t.Fatalf("a passed reset clock (reset_in_s 0) is anchored at the receipt: %+v", d)
	}
	if !u.Blocked || len(u.Blockers) != 2 || u.Blockers[1] != "model_blocked" || u.RetryInSec != 120 || u.RetryAt != "2026-10-08T12:02:00Z" {
		t.Fatalf("block = %v %v %d %q", u.Blocked, u.Blockers, u.RetryInSec, u.RetryAt)
	}
	// Nothing about the remote's plan, key or wallet exists to carry.
	if u.Plan != "" || u.KeyName != "" || u.Wallet != nil || u.Rate != nil || u.ObservedAt != "" || u.Unlimited ||
		len(u.UnlimitedModels) != 0 || len(u.BlockedModels) != 0 || u.CooldownSec != 0 {
		t.Fatalf("a coddy snapshot carries only what the projection says: %+v", u)
	}
	body, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"plan"`, `"keyName"`, `"wallet"`, `"rate"`, `"observedAt"`, `"unlimitedModels"`, `"blockedModels"`} {
		if strings.Contains(string(body), key) {
			t.Errorf("the wire form carries %s: %s", key, body)
		}
	}
}

func TestMapCoddyUsageWithoutABlockHasNoRetryTime(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	u := mapCoddyUsage(&llm.WireUsage{Supported: true, Windows: []llm.WireUsageWindow{}}, "remote", "coder", at)
	if u.Blocked || u.RetryAt != "" || u.RetryInSec != 0 || len(u.Windows) != 0 || u.Stale {
		t.Fatalf("empty projection = %+v", u)
	}
}

func TestMapCoddyUsageUnsupportedAnswerIsJustUnsupported(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	u := mapCoddyUsage(&llm.WireUsage{Supported: false}, "remote", "coder", at)
	want := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: "remote", ProviderType: "coddy", Model: "coder",
		FetchedAt: "2026-10-08T12:00:00Z", Unsupported: true,
	}
	gotJSON, _ := json.Marshal(u)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("unsupported = %s\n  want %s", gotJSON, wantJSON)
	}
}

func TestCoddyUsageFingerprintIsPerAliasAndPerCredential(t *testing.T) {
	t.Setenv("REMOTE_API_KEY", "")
	p := config.ProviderConfig{Name: "remote", Type: "coddy", APIBase: "https://relay.example/swarm/nodes/n1", APIKey: "k1"}
	a := providerUsageFingerprint(p, "coder", "", false)
	if a == "" || a != llm.CoddyUsageFingerprint(p, "coder") {
		t.Fatalf("coddy fingerprint = %q, want the llm one %q", a, llm.CoddyUsageFingerprint(p, "coder"))
	}
	if providerUsageFingerprint(p, "helper", "", false) == a {
		t.Errorf("two aliases of one row share a fingerprint")
	}
	rotated := p
	rotated.APIKey = "k2"
	if providerUsageFingerprint(rotated, "coder", "", false) == a {
		t.Errorf("a rotated key keeps the fingerprint")
	}
	if !providerUsageSource("coddy") || !providerUsageSource(" Coddy ") {
		t.Errorf("coddy must have a usage source")
	}
}

// --- one entry per (provider, alias) --------------------------------------

func TestCoddyUsageReadsOneEntryPerAlias(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageDocument(62, 777), nil)
	remote.set("helper", 200, coddyUsageDocument(30, 1200), nil)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})

	c := mustUsage(t, m, "remote/coder", false)
	if c.Provider != "remote" || c.ProviderType != "coddy" || c.Model != "coder" || c.Unsupported || c.Error != "" {
		t.Fatalf("coder header = %+v", c)
	}
	if s := findWindow(c, "session"); s == nil || s.UsedPercent != 62 || s.ResetInSec != 777 {
		t.Fatalf("coder session window = %+v", s)
	}
	h := mustUsage(t, m, "remote/helper", false)
	if h.Model != "helper" || findWindow(h, "session").UsedPercent != 30 {
		t.Fatalf("helper = %+v", h)
	}
	if remote.reads("coder") != 1 || remote.reads("helper") != 1 || remote.lastAuth() != "Bearer "+coddyUsageToken {
		t.Fatalf("reads coder=%d helper=%d auth=%q", remote.reads("coder"), remote.reads("helper"), remote.lastAuth())
	}
	if got := usageEntryKeys(m); len(got) != 2 || got[0] != "remote/coder" || got[1] != "remote/helper" {
		t.Fatalf("entries = %v", got)
	}

	// Inside the TTL both are served from their own entry, age-corrected.
	clock.advance(5 * time.Second)
	c = mustUsage(t, m, "remote/coder", false)
	if remote.total() != 2 || findWindow(c, "session").ResetInSec != 772 {
		t.Fatalf("cached coder: reads=%d window=%+v", remote.total(), findWindow(c, "session"))
	}

	// Past the TTL only the alias that is read asks again; the other keeps its
	// own entry, floor and clock.
	clock.advance(20 * time.Second)
	remote.set("coder", 200, coddyUsageDocument(70, 600), nil)
	c = mustUsage(t, m, "remote/coder", false)
	if remote.reads("coder") != 2 || remote.reads("helper") != 1 || findWindow(c, "session").UsedPercent != 70 {
		t.Fatalf("after the TTL: coder=%d helper=%d window=%+v", remote.reads("coder"), remote.reads("helper"), findWindow(c, "session"))
	}
	h = mustUsage(t, m, "remote/helper", true)
	if remote.reads("helper") != 2 || h.Model != "helper" {
		t.Fatalf("helper refresh: reads=%d update=%+v", remote.reads("helper"), h)
	}
	if n := remote.strayRequests(); n != 0 {
		t.Fatalf("%d requests reached a route other than the usage one", n)
	}
}

func TestCoddyUsageSendsNoCredentialWhenTheRowHasNone(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	m := newCoddyUsageManager(t, remote, &usageCapture{}, nil, func(cfg *config.Config) { cfg.Providers[0].APIKey = "" })
	if u := mustUsage(t, m, "remote/coder", false); u.Error != "" || u.Unsupported {
		t.Fatalf("an open remote must be readable: %+v", u)
	}
	if got := remote.lastAuth(); got != "" {
		t.Fatalf("Authorization = %q, want none", got)
	}
}

func TestCoddyUsageFloorAndBackoffArePerAlias(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})
	mustUsage(t, m, "remote/coder", false)
	mustUsage(t, m, "remote/helper", false)

	clock.advance(30 * time.Second)
	remote.set("coder", 503, `{"detail":"busy"}`, map[string]string{"Retry-After": "5"})
	u := mustUsage(t, m, "remote/coder", true)
	if u.Error != ProviderUsageErrorUnavailable || !u.Stale || findWindow(u, "session") == nil || remote.reads("coder") != 2 {
		t.Fatalf("a 503 keeps the numbers, stale: reads=%d update=%+v", remote.reads("coder"), u)
	}
	clock.advance(2 * time.Second)
	u = mustUsage(t, m, "remote/coder", true)
	if remote.reads("coder") != 2 || !u.RefreshPending || u.RefreshInSec != 13 {
		t.Fatalf("inside the backoff and the floor the read is deferred: reads=%d update=%+v", remote.reads("coder"), u)
	}
	// The other alias has an entry, a floor and a pause of its own.
	h := mustUsage(t, m, "remote/helper", true)
	if remote.reads("helper") != 2 || h.RefreshPending {
		t.Fatalf("helper must not wait for coder's pause: reads=%d update=%+v", remote.reads("helper"), h)
	}
	remote.set("coder", 200, coddyUsageDocument(44, 500), nil)
	clock.advance(14 * time.Second)
	waitUsageIdle(t, m)
	if remote.reads("coder") != 3 {
		t.Fatalf("the deferred read did not fire: coder reads=%d", remote.reads("coder"))
	}
	if u = mustUsage(t, m, "remote/coder", false); u.Stale || u.Error != "" || findWindow(u, "session").UsedPercent != 44 {
		t.Fatalf("after the deferred read: %+v", u)
	}
}

func TestCoddyUsageDeferredReadReportsToTheSessionWithTheAlias(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	ctx := context.Background()
	if _, err := m.ProviderUsageForSession(ctx, "s1", "remote/coder", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Second)
	u, err := m.ProviderUsageForSession(ctx, "s1", "remote/coder", true)
	if err != nil || u == nil || !u.RefreshPending || u.Model != "coder" {
		t.Fatalf("expected a deferred read naming the alias: err=%v update=%+v", err, u)
	}
	remote.set("coder", 200, coddyUsageDocument(55, 300), nil)
	clock.advance(11 * time.Second)
	waitUsageIdle(t, m)
	updates, ids := sender.snapshot()
	if len(updates) != 1 || ids[0] != "s1" || updates[0].Model != "coder" || findWindow(updates[0], "session").UsedPercent != 55 {
		t.Fatalf("deferred result: updates=%+v ids=%v", updates, ids)
	}
}

// --- names that select nothing --------------------------------------------

func TestCoddyUsageRefusesNamesThatSelectNoConfiguredRow(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	m := newCoddyUsageManager(t, remote, &usageCapture{}, nil, nil)
	ctx := context.Background()

	for _, name := range []string{"remote/ghost", "remote/coder/extra", "remote/Coder", "remote/stub"} {
		if u, err := m.ProviderUsage(ctx, name, false); !errors.Is(err, ErrProviderUsageUnknownModel) || u != nil {
			t.Errorf("ProviderUsage(%q) = %+v, %v; want ErrProviderUsageUnknownModel", name, u, err)
		}
		if u, err := m.ProviderUsageForSession(ctx, "s1", name, true); !errors.Is(err, ErrProviderUsageUnknownModel) || u != nil {
			t.Errorf("ProviderUsageForSession(%q) = %+v, %v; want ErrProviderUsageUnknownModel", name, u, err)
		}
	}
	// A coddy row named without a model has no usage of its own: unsupported,
	// not an error, and nothing is asked or remembered.
	for _, name := range []string{"remote", "remote/", " remote/ "} {
		for _, refresh := range []bool{false, true} {
			u, err := m.ProviderUsage(ctx, name, refresh)
			if err != nil || u == nil || !u.Unsupported || u.Disabled || u.Provider != "remote" || u.ProviderType != "coddy" || u.Model != "" {
				t.Errorf("ProviderUsage(%q, %v) = %+v, %v; want unsupported", name, refresh, u, err)
			}
		}
	}
	if _, err := m.ProviderUsage(ctx, "nowhere/coder", false); err == nil || errors.Is(err, ErrProviderUsageUnknownModel) {
		t.Errorf("an unknown provider keeps its own error: %v", err)
	}
	if keys := usageEntryKeys(m); len(keys) != 0 || remote.total() != 0 {
		t.Fatalf("a refused name must create no entry and read nothing: entries=%v reads=%d", keys, remote.total())
	}

	// A configured alias is read as usual; a type without a source ignores
	// whatever follows the slash.
	if u := mustUsage(t, m, " remote/coder ", false); u.Model != "coder" || remote.reads("coder") != 1 {
		t.Fatalf("trimmed selector: %+v reads=%d", u, remote.reads("coder"))
	}
	if u := mustUsage(t, m, "stub/whatever", false); !u.Unsupported || u.Model != "" || u.ProviderType != "openai" {
		t.Fatalf("stub selector = %+v", u)
	}
	if keys := usageEntryKeys(m); len(keys) != 1 || keys[0] != "remote/coder" {
		t.Fatalf("entries = %v", keys)
	}
}

func TestCoddyUsageSwitchedOffPanelIsNeverReadAndKeepsTheAlias(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	off := false
	m := newCoddyUsageManager(t, remote, sender, nil, func(cfg *config.Config) { cfg.Providers[0].UsageLimitsPanel = &off })
	u := mustUsage(t, m, "remote/coder", true)
	if !u.Unsupported || !u.Disabled || u.Model != "coder" || u.Provider != "remote" || u.ProviderType != "coddy" {
		t.Fatalf("switched-off row = %+v", u)
	}
	if _, err := m.ProviderUsage(context.Background(), "remote/ghost", false); !errors.Is(err, ErrProviderUsageUnknownModel) {
		t.Fatalf("an alias no row names is refused even when the panel is off: %v", err)
	}
	id := newUsageSession(t, m, "")
	m.HandleSessionReady(id)
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageIdle(t, m)
	if updates, _ := sender.snapshot(); len(updates) != 0 || remote.total() != 0 || len(usageEntryKeys(m)) != 0 {
		t.Fatalf("nothing is read or published: updates=%+v reads=%d entries=%v", updates, remote.total(), usageEntryKeys(m))
	}
}

// --- "unsupported" is remembered for a minute -----------------------------

func TestCoddyUsageRemembersUnsupportedForSixtySeconds(t *testing.T) {
	if coddyUsageUnsupportedTTL != 60*time.Second {
		t.Fatalf("coddyUsageUnsupportedTTL = %v, want 60s", coddyUsageUnsupportedTTL)
	}
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageUnsupportedBody, nil)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})

	u := mustUsage(t, m, "remote/coder", false)
	if !u.Unsupported || u.Model != "coder" || u.Provider != "remote" || u.ProviderType != "coddy" || u.Disabled || remote.reads("coder") != 1 {
		t.Fatalf("first read: %+v reads=%d", u, remote.reads("coder"))
	}
	// Past the 20 s TTL, inside the 60 s memory: answered without a request.
	clock.advance(25 * time.Second)
	if u = mustUsage(t, m, "remote/coder", false); !u.Unsupported || remote.reads("coder") != 1 {
		t.Fatalf("inside the memory: %+v reads=%d", u, remote.reads("coder"))
	}
	// A manual refresh ignores the memory and reads (the remote still says no).
	clock.advance(5 * time.Second)
	if u = mustUsage(t, m, "remote/coder", true); !u.Unsupported || remote.reads("coder") != 2 {
		t.Fatalf("manual refresh: %+v reads=%d", u, remote.reads("coder"))
	}
	// The memory restarted with that answer: 55 s later it still holds ...
	clock.advance(55 * time.Second)
	if u = mustUsage(t, m, "remote/coder", false); !u.Unsupported || remote.reads("coder") != 2 {
		t.Fatalf("restarted memory: %+v reads=%d", u, remote.reads("coder"))
	}
	// ... and 61 s after it the next automatic read asks again and sees a
	// lender that has enabled its reader meanwhile.
	clock.advance(6 * time.Second)
	remote.set("coder", 200, coddyUsageDocument(41, 900), nil)
	u = mustUsage(t, m, "remote/coder", false)
	if u.Unsupported || remote.reads("coder") != 3 || findWindow(u, "session") == nil || findWindow(u, "session").UsedPercent != 41 {
		t.Fatalf("after the memory: %+v reads=%d", u, remote.reads("coder"))
	}
}

func TestCoddyUsageRemembersOnlyJSONAnswersAsUnsupported(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	// A remote that predates the projection: the reserved route's JSON 404.
	remote.set("coder", 404, coddyUsageNotFound, nil)
	// Not a remote Coddy at all, or a proxy's page: a plain 404 says nothing.
	remote.set("helper", 404, coddyUsageHTML, map[string]string{"Content-Type": "text/html"})
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})

	u := mustUsage(t, m, "remote/coder", false)
	if !u.Unsupported || u.Error != "" {
		t.Fatalf("a JSON not_found 404 is unsupported: %+v", u)
	}
	clock.advance(30 * time.Second)
	if mustUsage(t, m, "remote/coder", false); remote.reads("coder") != 1 {
		t.Fatalf("remembered: reads=%d", remote.reads("coder"))
	}

	h := mustUsage(t, m, "remote/helper", false)
	if h.Unsupported || h.Error != ProviderUsageErrorUnavailable || h.Model != "helper" {
		t.Fatalf("an HTML 404 is a failed read, not an unsupported source: %+v", h)
	}
	clock.advance(21 * time.Second)
	remote.set("helper", 200, coddyUsageDocument(12, 100), nil)
	h = mustUsage(t, m, "remote/helper", false)
	if remote.reads("helper") != 2 || h.Unsupported || h.Error != "" || findWindow(h, "session") == nil {
		t.Fatalf("a failed read has no memory: reads=%d update=%+v", remote.reads("helper"), h)
	}
}

func TestCoddyUsageTurnEndAndSessionReadyStaySilentWhileUnsupportedIsRemembered(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageUnsupportedBody, nil)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	id := newUsageSession(t, m, "")

	// The read that finds out is delivered once ...
	m.HandleSessionReady(id)
	waitUsageIdle(t, m)
	updates, _ := sender.snapshot()
	if remote.reads("coder") != 1 || len(updates) != 1 || !updates[0].Unsupported || updates[0].Model != "coder" {
		t.Fatalf("discovery: reads=%d updates=%+v", remote.reads("coder"), updates)
	}
	// ... and then neither a turn end nor another session start says or asks
	// anything until the memory ends.
	clock.advance(30 * time.Second)
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	m.HandleSessionReady(id)
	waitUsageIdle(t, m)
	updates, _ = sender.snapshot()
	if remote.reads("coder") != 1 || len(updates) != 1 || clock.pending() != 0 {
		t.Fatalf("inside the memory: reads=%d updates=%+v pending=%d", remote.reads("coder"), updates, clock.pending())
	}
	clock.advance(31 * time.Second)
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageIdle(t, m)
	updates, _ = sender.snapshot()
	if remote.reads("coder") != 2 || len(updates) != 2 || !updates[1].Unsupported {
		t.Fatalf("after the memory: reads=%d updates=%+v", remote.reads("coder"), updates)
	}
	if clock.pending() != 0 {
		t.Fatalf("an unsupported answer arms no follow-up read: pending=%d", clock.pending())
	}
}

// --- a vanished alias, a refused key, a failed read -----------------------

func TestCoddyUsageUnknownModelDropsTheNumbersAndIsNotSticky(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageDocument(62, 777), nil)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})

	if u := mustUsage(t, m, "remote/coder", false); findWindow(u, "session") == nil {
		t.Fatalf("numbers: %+v", u)
	}
	clock.advance(30 * time.Second)
	remote.set("coder", 404, coddyUsageUnknownModel, nil)
	u := mustUsage(t, m, "remote/coder", true)
	if u.Error != ProviderUsageErrorInvalid || len(u.Windows) != 0 || u.Stale || u.Blocked || u.Model != "coder" || u.FetchedAt == "" {
		t.Fatalf("a vanished alias shows no numbers: %+v", u)
	}
	m.usage.mu.Lock()
	e := m.usage.entries["remote/coder"]
	sticky := e.unauthorized || !e.backoffUntil.IsZero()
	m.usage.mu.Unlock()
	if sticky {
		t.Fatalf("the drop must set no rejection mark and no backoff")
	}
	// Inside the TTL the empty answer is served; after it the alias is asked
	// again, and a lender that shares it once more is shown again.
	clock.advance(5 * time.Second)
	if u = mustUsage(t, m, "remote/coder", false); u.Error != ProviderUsageErrorInvalid || remote.reads("coder") != 2 {
		t.Fatalf("inside the TTL: reads=%d update=%+v", remote.reads("coder"), u)
	}
	clock.advance(16 * time.Second)
	remote.set("coder", 200, coddyUsageDocument(20, 400), nil)
	u = mustUsage(t, m, "remote/coder", false)
	if remote.reads("coder") != 3 || u.Error != "" || findWindow(u, "session") == nil || findWindow(u, "session").UsedPercent != 20 {
		t.Fatalf("not sticky: reads=%d update=%+v", remote.reads("coder"), u)
	}
}

func TestCoddyUsageTransportFailuresAndHopErrorsKeepTheStaleNumbers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		header map[string]string
	}{
		{"server error", 500, "boom", nil},
		{"gateway", 502, "bad gateway", nil},
		{"relay hop error", 404, coddyUsageHopError, nil},
		{"html 404", 404, coddyUsageHTML, map[string]string{"Content-Type": "text/html"}},
		{"bad request", 400, `{"status":400,"kind":"invalid","code":"bad_json","message":"m","emitted":false}`, nil},
		{"not a usage document", 200, `{"data":[]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := newCoddyUsageRemote(t)
			m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})
			mustUsage(t, m, "remote/coder", false)
			clock.advance(30 * time.Second)
			remote.set("coder", tc.status, tc.body, tc.header)
			u := mustUsage(t, m, "remote/coder", true)
			if u.Error != ProviderUsageErrorUnavailable || !u.Stale || findWindow(u, "session") == nil || u.Model != "coder" {
				t.Fatalf("the numbers must stay, marked stale: %+v", u)
			}
		})
	}
}

func TestCoddyUsageRejectedKeyIsStickyAndARotatedKeyIsAFreshEntry(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 401, `{"status":401,"kind":"auth","message":"no"}`, nil)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})

	u := mustUsage(t, m, "remote/coder", false)
	if u.Error != ProviderUsageErrorUnauthorized || len(u.Windows) != 0 || u.Model != "coder" || u.Provider != "remote" || remote.reads("coder") != 1 {
		t.Fatalf("first read: %+v reads=%d", u, remote.reads("coder"))
	}
	clock.advance(time.Minute)
	if mustUsage(t, m, "remote/coder", false); remote.reads("coder") != 1 {
		t.Fatalf("automatic reads must not retry a rejected key: reads=%d", remote.reads("coder"))
	}
	if mustUsage(t, m, "remote/coder", true); remote.reads("coder") != 2 {
		t.Fatalf("a manual refresh retries: reads=%d", remote.reads("coder"))
	}
	// A rotated key is a new fingerprint, so a new entry that starts at once.
	clock.advance(time.Second)
	remote.set("coder", 200, coddyUsageDocument(9, 90), nil)
	m.ReplaceConfig(coddyUsageConfig(m.activeCfg(), func(cfg *config.Config) { cfg.Providers[0].APIKey = "sk-rotated-token-0123456789" }))
	u = mustUsage(t, m, "remote/coder", false)
	if u.Error != "" || remote.reads("coder") != 3 || remote.lastAuth() != "Bearer sk-rotated-token-0123456789" || findWindow(u, "session") == nil {
		t.Fatalf("rotation: %+v reads=%d auth=%q", u, remote.reads("coder"), remote.lastAuth())
	}
}

// --- the provider's entries ------------------------------------------------

func TestCoddyUsageDropForgetsEveryAliasOfTheProviderOnly(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	m, clock := newCoddyUsageClockedManager(t, remote, &usageCapture{})
	mustUsage(t, m, "remote/coder", false)
	mustUsage(t, m, "remote/helper", false)
	// An entry of another provider stays.
	m.usage.mu.Lock()
	m.usage.entries["other"] = &providerUsageEntry{subject: usageSubject{provider: "other"}}
	m.usage.mu.Unlock()

	m.DropProviderUsage("remote")
	if keys := usageEntryKeys(m); len(keys) != 1 || keys[0] != "other" {
		t.Fatalf("entries after the drop = %v", keys)
	}
	// With the pacing history gone both aliases read at once, floor or not.
	clock.advance(time.Second)
	mustUsage(t, m, "remote/coder", true)
	mustUsage(t, m, "remote/helper", true)
	if remote.reads("coder") != 2 || remote.reads("helper") != 2 {
		t.Fatalf("reads coder=%d helper=%d", remote.reads("coder"), remote.reads("helper"))
	}
}

func TestCoddyUsageConfigSwapForgetsAliasesThatAreNoLongerConfigured(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	mustUsage(t, m, "remote/coder", false)
	mustUsage(t, m, "remote/helper", false)
	clock.advance(5 * time.Second)
	// A deferred refresh of the alias that is about to vanish.
	if u, _ := m.ProviderUsageForSession(context.Background(), "s1", "remote/helper", true); !u.RefreshPending {
		t.Fatalf("expected a deferred refresh: %+v", u)
	}

	m.ReplaceConfig(coddyUsageConfig(m.activeCfg(), func(cfg *config.Config) {
		cfg.Models = []config.ModelEntry{{Model: "remote/coder", MaxTokens: 1000}, {Model: "stub/model", MaxTokens: 1000}}
	}))
	if keys := usageEntryKeys(m); len(keys) != 1 || keys[0] != "remote/coder" {
		t.Fatalf("entries after the swap = %v", keys)
	}
	clock.advance(11 * time.Second)
	waitUsageIdle(t, m)
	if remote.reads("helper") != 1 {
		t.Fatalf("a removed alias is not read again: reads=%d", remote.reads("helper"))
	}
	if updates, _ := sender.snapshot(); len(updates) != 0 {
		t.Fatalf("updates = %+v", updates)
	}
	if _, err := m.ProviderUsage(context.Background(), "remote/helper", false); !errors.Is(err, ErrProviderUsageUnknownModel) {
		t.Fatalf("the removed alias is now refused: %v", err)
	}

	// The provider retyped: nothing of the coddy rows survives.
	m.ReplaceConfig(coddyUsageConfig(m.activeCfg(), func(cfg *config.Config) { cfg.Providers[0].Type = "openai" }))
	if keys := usageEntryKeys(m); len(keys) != 0 {
		t.Fatalf("entries after a retype = %v", keys)
	}
}

// --- the follow-up read ----------------------------------------------------

func TestCoddyUsageFollowUpReadAfterATurnEndFiresOnceAndDoesNotChain(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageDocument(62, 777), nil)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	id := newUsageSession(t, m, "")

	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageIdle(t, m)
	updates, ids := sender.snapshot()
	if remote.reads("coder") != 1 || len(updates) != 1 || ids[0] != id {
		t.Fatalf("turn end: reads=%d updates=%+v", remote.reads("coder"), updates)
	}
	if !updates[0].RefreshPending || updates[0].RefreshInSec != 20 || updates[0].Model != "coder" || clock.pending() != 1 {
		t.Fatalf("the first fetch must announce the follow-up a TTL later: update=%+v pending=%d", updates[0], clock.pending())
	}

	// The turn spent quota meanwhile; the remote's cache aged out.
	remote.set("coder", 200, coddyUsageDocument(71, 700), nil)
	clock.advance(20 * time.Second)
	waitUsageIdle(t, m)
	updates, ids = sender.snapshot()
	if remote.reads("coder") != 2 || len(updates) != 2 || ids[1] != id || findWindow(updates[1], "session").UsedPercent != 71 {
		t.Fatalf("follow-up: reads=%d updates=%+v ids=%v", remote.reads("coder"), updates, ids)
	}
	if updates[1].RefreshPending || clock.pending() != 0 {
		t.Fatalf("the follow-up arms no further read: update=%+v pending=%d", updates[1], clock.pending())
	}
	clock.advance(10 * time.Minute)
	waitUsageIdle(t, m)
	if remote.reads("coder") != 2 {
		t.Fatalf("nothing reads again without a trigger: reads=%d", remote.reads("coder"))
	}

	// A second turn end gets its own follow-up.
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageIdle(t, m)
	if remote.reads("coder") != 3 || clock.pending() != 1 {
		t.Fatalf("second turn end: reads=%d pending=%d", remote.reads("coder"), clock.pending())
	}
	clock.advance(20 * time.Second)
	waitUsageIdle(t, m)
	if remote.reads("coder") != 4 || clock.pending() != 0 {
		t.Fatalf("second follow-up: reads=%d pending=%d", remote.reads("coder"), clock.pending())
	}
}

func TestCoddyUsageFollowUpWaitsForAFetchThatTheFloorDeferred(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	id := newUsageSession(t, m, "")
	mustUsage(t, m, "remote/coder", false)
	clock.advance(5 * time.Second)

	// The turn ends inside the floor: the snapshot goes out at once, the fetch
	// is deferred to the floor's end, and that fetch owes the follow-up.
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	waitUsageIdle(t, m)
	updates, _ := sender.snapshot()
	if remote.reads("coder") != 1 || len(updates) != 1 || !updates[0].RefreshPending || updates[0].RefreshInSec != 10 || clock.pending() != 1 {
		t.Fatalf("deferred turn end: reads=%d updates=%+v pending=%d", remote.reads("coder"), updates, clock.pending())
	}
	clock.advance(10 * time.Second)
	waitUsageIdle(t, m)
	updates, _ = sender.snapshot()
	if remote.reads("coder") != 2 || len(updates) != 2 || !updates[1].RefreshPending || updates[1].RefreshInSec != 20 || clock.pending() != 1 {
		t.Fatalf("deferred fetch: reads=%d updates=%+v pending=%d", remote.reads("coder"), updates, clock.pending())
	}
	clock.advance(20 * time.Second)
	waitUsageIdle(t, m)
	if remote.reads("coder") != 3 || clock.pending() != 0 {
		t.Fatalf("follow-up: reads=%d pending=%d", remote.reads("coder"), clock.pending())
	}
}

func TestCoddyUsageTwoTurnEndsNeverHoldTwoPendingReads(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	id := newUsageSession(t, m, "")
	for i := 0; i < 2; i++ {
		if err := usagePrompt(t, m, id, sender, nil); err != nil {
			t.Fatal(err)
		}
		waitUsageIdle(t, m)
		clock.advance(time.Second)
		if clock.pending() > 1 {
			t.Fatalf("turn end %d left %d pending reads", i+1, clock.pending())
		}
	}
	for step := 0; step < 6 && clock.pending() > 0; step++ {
		clock.advance(20 * time.Second)
		waitUsageIdle(t, m)
		if clock.pending() > 1 {
			t.Fatalf("step %d: %d pending reads", step, clock.pending())
		}
	}
	if clock.pending() != 0 {
		t.Fatalf("the follow-ups chained: %d still pending", clock.pending())
	}
	// Two turn ends, no more than one read each plus the one a follow-up
	// brings: bounded, whatever the timing.
	if got := remote.reads("coder"); got < 2 || got > 4 {
		t.Fatalf("reads = %d", got)
	}
}

func TestCoddyUsageFailedOrRefusedFetchOwesNoFollowUp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"transport failure", 500, "boom"},
		{"rejected key", 401, `{"status":401,"kind":"auth","message":"no"}`},
		{"vanished alias", 404, coddyUsageUnknownModel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := newCoddyUsageRemote(t)
			remote.set("coder", tc.status, tc.body, nil)
			sender := &usageCapture{}
			m, clock := newCoddyUsageClockedManager(t, remote, sender)
			id := newUsageSession(t, m, "")
			if err := usagePrompt(t, m, id, sender, nil); err != nil {
				t.Fatal(err)
			}
			waitUsageIdle(t, m)
			if remote.reads("coder") != 1 || clock.pending() != 0 {
				t.Fatalf("reads=%d pending=%d", remote.reads("coder"), clock.pending())
			}
			clock.advance(10 * time.Minute)
			waitUsageIdle(t, m)
			if remote.reads("coder") != 1 {
				t.Fatalf("a failed read owes no follow-up: reads=%d", remote.reads("coder"))
			}
		})
	}
}

// A sticky rejection tells the turn without another request and arms nothing.
func TestCoddyUsageTurnEndAfterARejectionMakesNoRequest(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 401, `{"status":401,"kind":"auth","message":"no"}`, nil)
	sender := &usageCapture{}
	m, clock := newCoddyUsageClockedManager(t, remote, sender)
	id := newUsageSession(t, m, "")
	mustUsage(t, m, "remote/coder", false)
	for i := 0; i < 3; i++ {
		if err := usagePrompt(t, m, id, sender, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitUsageIdle(t, m)
	updates, _ := sender.snapshot()
	if remote.reads("coder") != 1 || clock.pending() != 0 || len(updates) != 3 || updates[0].Error != ProviderUsageErrorUnauthorized || updates[0].Model != "coder" {
		t.Fatalf("reads=%d pending=%d updates=%+v", remote.reads("coder"), clock.pending(), updates)
	}
}

// --- the sessions' own subject --------------------------------------------

func TestCoddyUsageTurnEndsOfTwoAliasesReadTheirOwnAlias(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	remote.set("coder", 200, coddyUsageDocument(62, 777), nil)
	remote.set("helper", 200, coddyUsageDocument(30, 1200), nil)
	sender := &usageCapture{}
	m, _ := newCoddyUsageClockedManager(t, remote, sender)
	coder := newUsageSession(t, m, "remote/coder")
	helper := newUsageSession(t, m, "remote/helper")
	for _, id := range []string{coder, helper} {
		if err := usagePrompt(t, m, id, sender, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitUsageIdle(t, m)
	updates, ids := sender.snapshot()
	got := map[string]acp.ProviderUsageUpdate{}
	for i, u := range updates {
		got[ids[i]] = u
	}
	if len(updates) != 2 || got[coder].Model != "coder" || findWindow(got[coder], "session").UsedPercent != 62 ||
		got[helper].Model != "helper" || findWindow(got[helper], "session").UsedPercent != 30 {
		t.Fatalf("updates = %+v ids = %v", updates, ids)
	}
	if remote.reads("coder") != 1 || remote.reads("helper") != 1 {
		t.Fatalf("reads coder=%d helper=%d", remote.reads("coder"), remote.reads("helper"))
	}
}

// --- concurrency -----------------------------------------------------------

func TestCoddyUsageConcurrentReadsTurnEndsAndSwapsAreRaceClean(t *testing.T) {
	remote := newCoddyUsageRemote(t)
	sender := &usageCapture{}
	m := newCoddyUsageManager(t, remote, sender, nil, nil)
	ids := []string{newUsageSession(t, m, "remote/coder"), newUsageSession(t, m, "remote/helper")}
	var wg sync.WaitGroup
	ctx := context.Background()
	names := []string{"remote/coder", "remote/helper", "remote/coder", "stub/model"}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				switch (g + i) % 5 {
				case 0, 1:
					_, _ = m.ProviderUsage(ctx, names[(g+i)%len(names)], i%7 == 0)
				case 2:
					_, _ = m.ProviderUsageForSession(ctx, ids[g%2], names[g%len(names)], true)
				case 3:
					// Concurrent prompts on one session may be refused as busy: only
					// the usage machinery behind them is under test.
					_, _ = m.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
						SessionID: ids[g%2], Prompt: []acp.ContentBlock{{Type: "text", Text: "hello"}},
					}, sender, nil)
				case 4:
					if g == 0 && i%10 == 4 {
						m.ReplaceConfig(coddyUsageConfig(m.activeCfg(), nil))
					}
					m.DropProviderUsage("remote")
				}
			}
		}(g)
	}
	wg.Wait()
	m.ShutdownProviderUsage(3 * time.Second)
}
