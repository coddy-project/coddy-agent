//go:build http

package httpserver

// GET /coddy/llm/models/{alias}/usage: the allowlist projection of the account
// usage behind a shared model (docs/plans/remote-model-provider-phase2.md, 4.1).
// The pure projection is tested on hand-built snapshots; the route is tested
// end to end on a remote whose provider is a NeuralDeep row read from a
// stand-in hub, with a needle scan of every body for what must never leave.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// What the remote's provider tells the hub's stand-in to report, none of which
// may reach a borrower.
const (
	usageNeedleTier     = "needle-tier-name"
	usageNeedleKey      = "needle-key-name"
	usageNeedleOptModel = "needle-option-model"
	usageNeedleOther    = "needle-other-model"
	usageNeedleBlocker  = "needle_model_blocker"
	usageUpstreamID     = "qwen3-secret" // the model id behind the alias "coder"
	usagePrivateID      = "private-model"
	usageSecondID       = "second-upstream"
	usageSecondAlias    = "second"
	usageHubKey         = "hub-secret-key-0123"
)

// usageHub is a stand-in for the hub's GET /limits, counting what asked it.
type usageHub struct {
	srv *httptest.Server

	mu     sync.Mutex
	status int
	body   string
	hits   int
	auths  []string
}

// newUsageHub starts the stand-in and points the NeuralDeep source at it.
func newUsageHub(t *testing.T) *usageHub {
	t.Helper()
	h := &usageHub{status: http.StatusOK, body: usageHubBody(nil)}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		h.hits++
		h.auths = append(h.auths, r.Header.Get("Authorization"))
		status, body := h.status, h.body
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(h.srv.Close)
	t.Setenv(llm.EnvNeuralDeepBaseURL, h.srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	return h
}

func (h *usageHub) set(status int, body string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status, h.body = status, body
}

func (h *usageHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

// usageHubBody renders the hub's schema-1 answer for a pro wallet key with a
// session counter at 407 of 15000, carrying the plan, the key name, the option
// models and the wallet that the projection must drop. blocked lists
// {model, reset_in_sec} gates.
func usageHubBody(blocked []usageHubGate) string {
	gates := make([]map[string]any, 0, len(blocked))
	for _, g := range blocked {
		gates = append(gates, map[string]any{
			"model": g.model, "blocker": usageNeedleBlocker,
			"resets_at": "2026-12-31T00:00:00+00:00", "reset_in_sec": g.resetIn,
		})
	}
	doc := map[string]any{
		"schema": 1, "observed_at": "2026-09-06T17:47:02Z", "tier": usageNeedleTier, "tier_expires_at": nil,
		"unlimited_volume": false, "bypass": false, "fair_use": true,
		"blocked_models": gates,
		"options": []map[string]any{{
			"code": "needle_opt", "title": "needle title", "models": []string{usageNeedleOptModel},
			"rpm": map[string]any{"limit": 60, "used": 0, "remaining": 60, "window": "1m"}, "unlimited_volume": true, "scope": "account",
		}},
		"key":      map[string]any{"name": usageNeedleKey, "status": "ok", "billing_mode": "wallet", "cap": nil},
		"decision": map[string]any{"scope": "chat", "can_request": true, "blockers": []string{}, "retry_after_sec": nil},
		"chat": map[string]any{
			"session":      map[string]any{"used": 407, "limit": 15000, "remaining": 14593, "reset_in_sec": 777, "resets_at": "2026-09-06T17:59:59Z", "window": "3h"},
			"week":         map[string]any{"used": 9981, "limit": 150000, "remaining": 140019, "reset_in_sec": 22378, "resets_at": "2026-09-07T00:00:00Z", "window": "iso-week"},
			"rpm":          map[string]any{"used": 2, "limit": 120, "remaining": 118, "reset_in_sec": 58},
			"cooldown_sec": 0, "scope": "account",
		},
		"daily_capacity": map[string]any{"pct_used": 12.5, "exhausted": false, "resets_at": "2026-09-07T00:00:00+00:00"},
		"wallet":         map[string]any{"balance_rub": -7777.77, "spent_rub_30d": 4242.42},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

type usageHubGate struct {
	model   string
	resetIn int
}

// withUsageProvider turns the remote's provider into a NeuralDeep row with an
// explicit key (the stand-in hub answers it), shares a second model under
// "second" and keeps the private one private.
func withUsageProvider(panel *bool) sharedFixtureOption {
	return withSharedConfig(func(c *config.Config) {
		c.Providers[0] = config.ProviderConfig{Name: "stub", Type: "neuraldeep", APIKey: usageHubKey, UsageLimitsPanel: panel}
		c.Models = append(c.Models, config.ModelEntry{Model: "stub/" + usageSecondID, MaxTokens: 1024, MaxContextTokens: 1000, SharedAs: usageSecondAlias})
	})
}

// getUsage reads the usage route of alias with token and decodes nothing: the
// caller looks at the raw body.
func (fx *sharedFixture) getUsage(alias, token string) (int, http.Header, string) {
	fx.t.Helper()
	resp := fx.get("/coddy/llm/models/"+alias+"/usage", token)
	raw := bodyString(fx.t, resp)
	return resp.StatusCode, resp.Header, raw
}

func decodeWireUsage(t *testing.T, raw string) llm.WireUsage {
	t.Helper()
	var u llm.WireUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("not a usage document: %v: %s", err, raw)
	}
	return u
}

// mustNotMention fails when body carries any of the needles.
func mustNotMention(t *testing.T, what, body string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if n != "" && strings.Contains(body, n) {
			t.Fatalf("%s mentions %q: %s", what, n, body)
		}
	}
}

// The values every body is scanned for: whatever the lender's hub reports about
// the account that is not a percentage, a label, a reset delay or a blocker id.
var usageNeverOnTheWire = []string{
	usageNeedleTier, usageNeedleKey, usageNeedleOptModel, usageNeedleOther, usageNeedleBlocker, "needle_opt", "needle title",
	"7777", "4242", "15000", "150000", "14593", "140019", "9981",
	usageUpstreamID, usagePrivateID, usageSecondID, usageHubKey, "upstream-secret-key", "stub", sharedTestUpstreamAB,
	"resets_at", "resetsAt", "observed", "provider", "plan", "wallet", "keyName", "key_name", "unlimited", "rate", "cooldown",
}

// ---------------------------------------------------------------------------
// The route
// ---------------------------------------------------------------------------

func TestSharedUsageRouteAnswersTheAllowlistProjection(t *testing.T) {
	hub := newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))

	status, header, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type %q", got)
	}
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q", got)
	}
	mustNotMention(t, "the usage body", raw, usageNeverOnTheWire...)

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"supported", "account_wide", "stale", "windows", "blocked", "blockers", "retry_in_s"}
	for k := range doc {
		if !slices.Contains(allowed, k) {
			t.Fatalf("the body carries %q, which is not on the allowlist: %s", k, raw)
		}
	}

	u := decodeWireUsage(t, raw)
	if !u.Supported || !u.AccountWide || u.Stale || u.Blocked || len(u.Blockers) != 0 || u.RetryInS != 0 {
		t.Fatalf("document: %+v", u)
	}
	byID := map[string]llm.WireUsageWindow{}
	for _, w := range u.Windows {
		byID[w.ID] = w
	}
	if len(u.Windows) != 3 || byID["session"].ID == "" || byID["week"].ID == "" || byID["day"].ID == "" {
		t.Fatalf("windows: %+v", u.Windows)
	}
	if got := byID["session"]; got.Label != "3h" || math.Abs(got.UsedPercent-407.0/15000.0*100) > 0.001 || got.Exhausted {
		t.Fatalf("session window: %+v", got)
	}
	if got := byID["week"]; got.Label != "week" || math.Abs(got.UsedPercent-9981.0/150000.0*100) > 0.001 {
		t.Fatalf("week window: %+v", got)
	}
	if got := byID["day"]; got.Label != "day" || got.UsedPercent != 12.5 {
		t.Fatalf("day window: %+v", got)
	}
	// The reset is a relative time that the remote has already corrected for the
	// snapshot's age; nothing absolute is on the wire.
	for id, want := range map[string]int{"session": 777, "week": 22378, "day": 22378} {
		w := byID[id]
		if w.ResetInS == nil || *w.ResetInS > want || *w.ResetInS < want-5 {
			t.Fatalf("%s reset_in_s %v, want about %d", id, w.ResetInS, want)
		}
	}
	if hub.count() != 1 {
		t.Fatalf("the hub was asked %d times", hub.count())
	}
	hub.mu.Lock()
	auth := hub.auths[0]
	hub.mu.Unlock()
	if auth != "Bearer "+usageHubKey {
		t.Fatalf("the hub was asked with %q", auth)
	}
}

// Any number of borrowers, aliases and requests share the manager's own cache,
// and ?refresh=1 is not a way to make the lender read its account more often.
func TestSharedUsageRouteIsServedFromTheManagersCache(t *testing.T) {
	hub := newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))

	for _, path := range []string{
		"/coddy/llm/models/" + sharedTestAlias + "/usage",
		"/coddy/llm/models/" + sharedTestAlias + "/usage",
		"/coddy/llm/models/" + usageSecondAlias + "/usage",
		"/coddy/llm/models/" + sharedTestAlias + "/usage?refresh=1",
		"/coddy/llm/models/" + sharedTestAlias + "/usage?refresh=true&force=1",
	} {
		for _, token := range []string{sharedTestSharedTok, sharedTestMainToken} {
			resp := fx.get(path, token)
			if raw := bodyString(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"supported":true`) {
				t.Fatalf("%s with %s: %d %s", path, token, resp.StatusCode, raw)
			}
		}
	}
	if n := hub.count(); n != 1 {
		t.Fatalf("the hub was asked %d times for ten reads of two aliases of one account, want 1", n)
	}
}

// The reset of a window is relative to the moment the node answers: a cached
// snapshot that is ten seconds old answers ten seconds less, a hub that gives a
// delay and no absolute time still gives a reset, and a window with no clock
// gives none.
func TestSharedUsageResetIsAgeCorrectedAndRelative(t *testing.T) {
	hub := newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	now := time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)
	var clockMu sync.Mutex
	fx.srv.mgr.SetProviderUsageClock(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}, func(time.Duration, func()) func() bool { return func() bool { return true } })

	// The session window has a delay and no absolute time, the week window both,
	// the daily capacity neither.
	var doc map[string]any
	if err := json.Unmarshal([]byte(usageHubBody(nil)), &doc); err != nil {
		t.Fatal(err)
	}
	chat := doc["chat"].(map[string]any)
	delete(chat["session"].(map[string]any), "resets_at")
	delete(doc["daily_capacity"].(map[string]any), "resets_at")
	raw, _ := json.Marshal(doc)
	hub.set(http.StatusOK, string(raw))

	resets := func() map[string]*int {
		t.Helper()
		status, _, body := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
		if status != http.StatusOK {
			t.Fatalf("status %d: %s", status, body)
		}
		mustNotMention(t, "the usage body", body, "resets_at", "resetsAt", "2026")
		out := map[string]*int{}
		for _, w := range decodeWireUsage(t, body).Windows {
			out[w.ID] = w.ResetInS
		}
		return out
	}
	got := resets()
	if got["session"] == nil || *got["session"] != 777 || got["week"] == nil || *got["week"] != 22378 || got["day"] != nil {
		t.Fatalf("fresh snapshot: session %v week %v day %v", got["session"], got["week"], got["day"])
	}

	clockMu.Lock()
	now = now.Add(10 * time.Second)
	clockMu.Unlock()
	got = resets()
	if got["session"] == nil || *got["session"] != 767 || got["week"] == nil || *got["week"] != 22368 || got["day"] != nil {
		t.Fatalf("snapshot of ten seconds ago: session %v week %v day %v", got["session"], got["week"], got["day"])
	}
	if hub.count() != 1 {
		t.Fatalf("the hub was asked %d times: the second answer is the cache aged", hub.count())
	}
}

// A block that concerns one model is named under its alias only, whatever the
// hub says about the models of the rest of the account, and what the hub calls
// the gate never leaves.
func TestSharedUsageBlockNamesTheAliasOnly(t *testing.T) {
	hub := newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	hub.set(http.StatusOK, usageHubBody([]usageHubGate{
		{model: usageUpstreamID, resetIn: 3600}, // the model behind "coder"
		{model: usageNeedleOther, resetIn: 7200},
		{model: usagePrivateID, resetIn: 9000}, // a model that is not shared at all
	}))

	status, _, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	mustNotMention(t, "the blocked alias", raw, usageNeverOnTheWire...)
	u := decodeWireUsage(t, raw)
	if !u.Blocked || !slices.Equal(u.Blockers, []string{"model_blocked"}) || u.RetryInS > 3600 || u.RetryInS < 3590 {
		t.Fatalf("the shared alias is not reported blocked as model_blocked with its own delay: %+v", u)
	}

	// The second alias is a different model of the same account: not blocked.
	status, _, raw = fx.getUsage(usageSecondAlias, sharedTestSharedTok)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	mustNotMention(t, "the other alias", raw, usageNeverOnTheWire...)
	if u := decodeWireUsage(t, raw); u.Blocked || len(u.Blockers) != 0 || u.RetryInS != 0 {
		t.Fatalf("a block of another model reached the other alias: %+v", u)
	}
}

// A gate on a model nobody shares is dropped altogether.
func TestSharedUsageDropsTheBlockOfAnUnsharedModel(t *testing.T) {
	hub := newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	hub.set(http.StatusOK, usageHubBody([]usageHubGate{{model: usagePrivateID, resetIn: 9000}, {model: usageNeedleOther, resetIn: 100}}))
	_, _, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
	mustNotMention(t, "the usage body", raw, usageNeverOnTheWire...)
	if u := decodeWireUsage(t, raw); u.Blocked || len(u.Blockers) != 0 || u.RetryInS != 0 || !u.Supported {
		t.Fatalf("the block of an unshared model reached the alias: %s", raw)
	}
}

// An alias nobody shares, a private model, a selector and a made-up name all
// answer the same 404 that names nothing the client did not send.
func TestSharedUsageUnknownAliasAnswers404NamingNothing(t *testing.T) {
	newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	for _, alias := range []string{"nope", "private-model", "stub%2Fqwen3-secret", "stub%2Fprivate-model", "qwen3-secret", "Coder", "%20"} {
		resp := fx.get("/coddy/llm/models/"+alias+"/usage", sharedTestSharedTok)
		raw := bodyString(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%q: status %d: %s", alias, resp.StatusCode, raw)
		}
		var e llm.WireError
		if err := json.Unmarshal([]byte(raw), &e); err != nil || e.Kind != llm.WireKindInvalid || e.Code != llm.WireCodeUnknownModel {
			t.Fatalf("%q: not an unknown_model answer: %s", alias, raw)
		}
		mustNotMention(t, "the 404 for "+alias, raw, "stub", usageUpstreamID, usagePrivateID, usageHubKey, "nope", "neuraldeep")
	}
}

// A usage read takes no stream slot and is not counted by max_streams: it
// answers while every slot of the credential is held by a call, and holds none.
func TestSharedUsageReadTakesNoStreamSlot(t *testing.T) {
	newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil), withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	hold := make(chan struct{})
	entered := make(chan struct{}, 1)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return &llm.Response{Content: "x"}, nil
	}
	reader := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	<-entered
	if fx.slotsInUse() != 1 {
		t.Fatalf("slots in use: %d", fx.slotsInUse())
	}
	// The only slot is taken: another call is refused, a usage read is not.
	if e := readError(t, fx.complete(wireReq(sharedTestAlias))); e.Kind != llm.WireKindBusy {
		t.Fatalf("the second call was not refused as busy: %+v", e)
	}
	for i := 0; i < 3; i++ {
		status, _, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
		if status != http.StatusOK || !strings.Contains(raw, `"supported":true`) {
			t.Fatalf("usage read %d while the slot was held: %d %s", i, status, raw)
		}
	}
	if fx.slotsInUse() != 1 {
		t.Fatalf("usage reads changed the slots in use: %d", fx.slotsInUse())
	}
	close(hold)
	reader.all()
	waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
}

// A lender whose row has no usage source, or whose operator switched the panel
// off, answers "unsupported" and reads nothing.
func TestSharedUsageUnsupportedProviders(t *testing.T) {
	hub := newUsageHub(t)

	plain := newSharedFixture(t) // an openai row: no source
	status, _, raw := plain.getUsage(sharedTestAlias, sharedTestSharedTok)
	if status != http.StatusOK || strings.TrimSpace(raw) != `{"supported":false}` {
		t.Fatalf("a provider without a source: %d %s", status, raw)
	}

	off := false
	quiet := newSharedFixture(t, withUsageProvider(&off))
	status, _, raw = quiet.getUsage(sharedTestAlias, sharedTestSharedTok)
	if status != http.StatusOK || strings.TrimSpace(raw) != `{"supported":false}` {
		t.Fatalf("a switched-off panel: %d %s", status, raw)
	}
	if hub.count() != 0 {
		t.Fatalf("the hub was asked %d times for rows that report nothing", hub.count())
	}
}

// A server without a manager has no usage to report.
func TestSharedUsageWithoutAManagerIsUnsupported(t *testing.T) {
	fx := newSharedFixture(t)
	bare := New(fx.cfg, nil, fx.log, fx.home)
	ts := httptest.NewServer(bare.Handler())
	t.Cleanup(ts.Close)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/coddy/llm/models/"+sharedTestAlias+"/usage", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sharedTestSharedTok)
	resp, err := sharedTestClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if raw := bodyString(t, resp); resp.StatusCode != http.StatusOK || strings.TrimSpace(raw) != `{"supported":false}` {
		t.Fatalf("no manager: %d %s", resp.StatusCode, raw)
	}
}

// A failed read with nothing cached is a stale, empty, supported answer: the
// lender's credential state is not what a borrower learns.
func TestSharedUsageFailedReadAnswersStaleWithoutAKind(t *testing.T) {
	for name, status := range map[string]int{"unavailable": http.StatusInternalServerError, "unauthorized": http.StatusUnauthorized, "bad gateway": http.StatusBadGateway} {
		t.Run(name, func(t *testing.T) {
			hub := newUsageHub(t)
			hub.set(status, `{"detail":"`+usageHubKey+` is rejected for `+usageNeedleKey+`"}`)
			fx := newSharedFixture(t, withUsageProvider(nil))
			code, _, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
			if code != http.StatusOK {
				t.Fatalf("status %d: %s", code, raw)
			}
			mustNotMention(t, "the stale answer", raw, usageNeverOnTheWire...)
			mustNotMention(t, "the stale answer", raw, "unauthorized", "unavailable", "error", "rejected", "detail")
			u := decodeWireUsage(t, raw)
			if !u.Supported || !u.Stale || len(u.Windows) != 0 || u.Blocked {
				t.Fatalf("document: %s", raw)
			}
			if !strings.Contains(raw, `"windows":[]`) {
				t.Fatalf("windows must be an empty array, not null: %s", raw)
			}
		})
	}
}

// A hub that answers 403 says the account is blocked: that is a block of the
// account, reported as one with the fixed id.
func TestSharedUsageAnAccountBlockIsReported(t *testing.T) {
	hub := newUsageHub(t)
	hub.set(http.StatusForbidden, `{"detail":"blocked"}`)
	fx := newSharedFixture(t, withUsageProvider(nil))
	_, _, raw := fx.getUsage(sharedTestAlias, sharedTestSharedTok)
	u := decodeWireUsage(t, raw)
	if !u.Supported || !u.Blocked || !slices.Equal(u.Blockers, []string{"user_blocked"}) {
		t.Fatalf("document: %s", raw)
	}
}

// The gate: the LLM-only token and the main token read, an unknown token and an
// anonymous caller are refused as everywhere, and a node with no credential at
// all offers nothing.
func TestSharedUsageRouteFollowsTheGate(t *testing.T) {
	newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	path := "/coddy/llm/models/" + sharedTestAlias + "/usage"
	for token, want := range map[string]int{
		sharedTestSharedTok: http.StatusOK,
		sharedTestMainToken: http.StatusOK,
		"some-unknown":      http.StatusUnauthorized,
		"":                  http.StatusUnauthorized,
	} {
		if got := fx.status(http.MethodGet, path, token, nil); got != want {
			t.Fatalf("token %q: %d, want %d", token, got, want)
		}
	}

	open := newSharedFixture(t, withUsageProvider(nil), withSharedConfig(noCredentials))
	resp := open.get(path, "")
	e := readError(t, resp)
	if resp.StatusCode != http.StatusForbidden || e.Kind != llm.WireKindAuth || !strings.Contains(e.Message, "shared models need authentication") {
		t.Fatalf("an open node: status %d: %+v", resp.StatusCode, e)
	}
	insecure := newSharedFixture(t, withUsageProvider(nil), withSharedConfig(func(c *config.Config) {
		noCredentials(c)
		c.HTTPServer.AllowInsecure = true
	}))
	if got := insecure.status(http.MethodGet, path, "", nil); got != http.StatusOK {
		t.Fatalf("allow_insecure: %d", got)
	}
}

// What the route did not know before: a path under /coddy/llm/ that is not one
// of the three routes still answers what it did, and the usage of a shared
// token on it is not a way into anything else.
func TestSharedUsageRouteLeavesTheOtherRoutesAlone(t *testing.T) {
	newUsageHub(t)
	fx := newSharedFixture(t, withUsageProvider(nil))
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/coddy/llm/models/coder"},
		{http.MethodGet, "/coddy/llm/models/coder/usage/extra"},
		{http.MethodGet, "/coddy/llm/completions"},
		{http.MethodGet, "/coddy/llm/nope"},
		{http.MethodPost, "/coddy/llm/models/coder/usage"},
		{http.MethodGet, "/coddy/sessions"},
	} {
		unknown := fx.status(tc.method, tc.path, "some-unknown-token", nil)
		if got := fx.status(tc.method, tc.path, sharedTestSharedTok, nil); got != unknown {
			t.Fatalf("%s %s: the shared token gets %d, an unknown token %d", tc.method, tc.path, got, unknown)
		}
	}
}

// ---------------------------------------------------------------------------
// The projection
// ---------------------------------------------------------------------------

func projectionRow() *config.ModelEntry {
	return &config.ModelEntry{Model: "stub/" + usageUpstreamID, SharedAs: sharedTestAlias}
}

var projectionNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func intp(v int) *int { return &v }

func TestProjectSharedUsageWindows(t *testing.T) {
	u := &acp.ProviderUsageUpdate{
		Provider: "stub", ProviderType: "codex", Plan: "pro-needle", FetchedAt: projectionNow.Format(time.RFC3339),
		Windows: []acp.UsageWindow{
			{ID: "session", Label: "5h", UsedPercent: 40, Used: intp(4), Limit: intp(10), Remaining: intp(6), ResetsAt: "2026-10-08T13:00:00Z", ResetInSec: 3600},
			{ID: "codex:0:session", Label: "GPT-Needle-Codex · 5h", UsedPercent: 99, ResetsAt: "2026-10-08T13:00:00Z", ResetInSec: 10},
			{ID: "week", Label: "week", UsedPercent: 150, ResetsAt: "2026-10-09T13:00:00Z", ResetInSec: 90000},
			{ID: "window-1209600", Label: "336h", UsedPercent: -5, ResetInSec: 120},
			{ID: "session-secondary", Label: "Pro plan 5h", UsedPercent: 10, Exhausted: true},
			{ID: "acu", Label: "ACU", UsedPercent: 33.3},
			{ID: "day", Label: "3H", UsedPercent: 1, ResetsAt: "2026-10-08T11:59:00Z", ResetInSec: 0},
			{ID: "weird", Label: "5h", UsedPercent: 5},
			{ID: "needle", Label: "day", UsedPercent: 5},
		},
	}
	w := projectSharedUsage(u, nil, projectionRow(), projectionNow)
	if !w.Supported || !w.AccountWide || w.Stale || w.Blocked {
		t.Fatalf("document: %+v", w)
	}
	type row struct {
		id, label string
		pct       float64
		reset     *int
		exhausted bool
	}
	var got []row
	for _, x := range w.Windows {
		got = append(got, row{x.ID, x.Label, x.UsedPercent, x.ResetInS, x.Exhausted})
	}
	want := []row{
		{"session", "5h", 40, intp(3600), false},
		{"week", "week", 100, intp(90000), false},
		{"window-1209600", "336h", 0, intp(120), false},
		{"session-secondary", "session-secondary", 10, nil, true}, // a label that is not a duration is replaced by the id
		{"acu", "ACU", 33.3, nil, false},
		{"day", "day", 1, intp(0), false}, // "3H" is not a label; an anchored clock that passed is 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("windows:\n got %+v\nwant %+v", got, want)
	}
	body, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	mustNotMention(t, "the projection", string(body), "pro-needle", "Needle", "needle", "codex:", "resets_at", "resetsAt", "stub", "codex", `"used":`, `"limit":`)
	// A window with no reset clock has no reset_in_s at all; one whose clock ran out has 0.
	if strings.Count(string(body), `"reset_in_s"`) != 4 || !strings.Contains(string(body), `"reset_in_s":0`) {
		t.Fatalf("reset_in_s presence: %s", body)
	}
}

// The presence rule of reset_in_s: an absolute clock gives the value as the
// remote delivers it, 0 once it has passed; a relative-only clock gives it
// while it runs and nothing after; no clock gives nothing.
func TestProjectSharedUsageResetPresence(t *testing.T) {
	cases := []struct {
		name string
		w    acp.UsageWindow
		want *int
	}{
		{"absolute and running", acp.UsageWindow{ID: "session", ResetsAt: "2026-10-08T13:00:00Z", ResetInSec: 600}, intp(600)},
		{"absolute and passed", acp.UsageWindow{ID: "session", ResetsAt: "2026-10-08T11:00:00Z"}, intp(0)},
		{"relative only, running (a hub's reset_in_sec without resets_at)", acp.UsageWindow{ID: "session", ResetInSec: 42}, intp(42)},
		{"relative only, run out", acp.UsageWindow{ID: "session"}, nil},
		{"no clock at all", acp.UsageWindow{ID: "acu", UsedPercent: 5}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := projectSharedUsage(&acp.ProviderUsageUpdate{Windows: []acp.UsageWindow{tc.w}}, nil, projectionRow(), projectionNow)
			if len(w.Windows) != 1 {
				t.Fatalf("windows: %+v", w.Windows)
			}
			got := w.Windows[0].ResetInS
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("reset_in_s %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProjectSharedUsageNeverEmitsNaNOrAnAbsoluteTime(t *testing.T) {
	u := &acp.ProviderUsageUpdate{Windows: []acp.UsageWindow{
		{ID: "session", UsedPercent: math.NaN(), ResetsAt: "2026-10-08T13:00:00Z", ResetInSec: 5},
		{ID: "week", UsedPercent: math.Inf(1)},
	}}
	w := projectSharedUsage(u, nil, projectionRow(), projectionNow)
	body, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("a hostile percentage broke the encoder: %v", err)
	}
	if w.Windows[0].UsedPercent != 0 || w.Windows[1].UsedPercent != 100 {
		t.Fatalf("percentages: %+v", w.Windows)
	}
	mustNotMention(t, "the projection", string(body), "2026", "T13:")
}

func TestProjectSharedUsageUnlimited(t *testing.T) {
	windows := []acp.UsageWindow{{ID: "session", UsedPercent: 90, ResetInSec: 10}}
	for name, u := range map[string]*acp.ProviderUsageUpdate{
		"the account is unlimited": {Unlimited: true, Windows: windows},
		"the model is in the list": {UnlimitedModels: []string{"other", "  " + strings.ToUpper(usageUpstreamID) + " "}, Windows: windows},
		"the model's case differs": {UnlimitedModels: []string{"Qwen3-Secret"}, Windows: windows},
	} {
		t.Run(name, func(t *testing.T) {
			w := projectSharedUsage(u, nil, projectionRow(), projectionNow)
			if len(w.Windows) != 0 {
				t.Fatalf("windows: %+v", w.Windows)
			}
			body, _ := json.Marshal(w)
			mustNotMention(t, "the projection", string(body), usageUpstreamID, "other", "unlimited")
			if !strings.Contains(string(body), `"windows":[]`) {
				t.Fatalf("windows must be an empty array: %s", body)
			}
		})
	}
	// A list of other models changes nothing.
	u := &acp.ProviderUsageUpdate{UnlimitedModels: []string{"somebody-else"}, Windows: windows}
	if w := projectSharedUsage(u, nil, projectionRow(), projectionNow); len(w.Windows) != 1 {
		t.Fatalf("a model that is not in the list lost its windows: %+v", w.Windows)
	}
}

func TestProjectSharedUsageStale(t *testing.T) {
	for name, tc := range map[string]struct {
		u   *acp.ProviderUsageUpdate
		err error
	}{
		"the snapshot is stale":      {u: &acp.ProviderUsageUpdate{Stale: true}},
		"the read reported an error": {u: &acp.ProviderUsageUpdate{Error: "unauthorized"}},
		"the read failed":            {u: &acp.ProviderUsageUpdate{}, err: errors.New("boom: " + usageHubKey)},
		"nothing came back":          {u: nil},
		"nothing and an error":       {u: nil, err: context.Canceled},
	} {
		t.Run(name, func(t *testing.T) {
			w := projectSharedUsage(tc.u, tc.err, projectionRow(), projectionNow)
			if !w.Supported || !w.Stale || len(w.Windows) != 0 {
				t.Fatalf("document: %+v", w)
			}
			body, _ := json.Marshal(w)
			mustNotMention(t, "the projection", string(body), "boom", usageHubKey, "unauthorized", "error")
		})
	}
	if w := projectSharedUsage(&acp.ProviderUsageUpdate{}, nil, projectionRow(), projectionNow); w.Stale {
		t.Fatalf("a clean snapshot is marked stale: %+v", w)
	}
	// An unsupported snapshot is not a usage document.
	if w := projectSharedUsage(&acp.ProviderUsageUpdate{Unsupported: true, Windows: []acp.UsageWindow{{ID: "session"}}}, nil, projectionRow(), projectionNow); w.Supported || len(w.Windows) != 0 {
		t.Fatalf("an unsupported snapshot projected as %+v", w)
	}
}

func TestProjectSharedUsageBlockers(t *testing.T) {
	u := &acp.ProviderUsageUpdate{
		Blocked:    true,
		Blockers:   []string{"session_exhausted", "kimi_budget_exhausted", "wallet_empty", "another-new-id", "session_exhausted", "quota_exhausted"},
		RetryInSec: 300,
	}
	w := projectSharedUsage(u, nil, projectionRow(), projectionNow)
	if !w.Blocked || w.RetryInS != 300 {
		t.Fatalf("document: %+v", w)
	}
	want := []string{"session_exhausted", "other", "wallet_empty", "quota_exhausted"}
	if !slices.Equal(w.Blockers, want) {
		t.Fatalf("blockers %v, want %v", w.Blockers, want)
	}
	// Every id of the fixed set passes through as it is.
	all := []string{"session_exhausted", "week_exhausted", "rpm_exhausted", "session_cooldown", "abuse_cooldown", "daily_capacity_exhausted",
		"key_blocked", "key_cap_blocked", "wallet_empty", "user_blocked", "quota_exhausted"}
	if w := projectSharedUsage(&acp.ProviderUsageUpdate{Blocked: true, Blockers: all}, nil, projectionRow(), projectionNow); !slices.Equal(w.Blockers, all) {
		t.Fatalf("the fixed set did not pass through: %v", w.Blockers)
	}
	// A retry delay of an account that is not blocked says nothing.
	if w := projectSharedUsage(&acp.ProviderUsageUpdate{RetryInSec: 99}, nil, projectionRow(), projectionNow); w.RetryInS != 0 || w.Blocked {
		t.Fatalf("an unblocked account carries a retry: %+v", w)
	}
}

func TestProjectSharedUsageModelBlock(t *testing.T) {
	fetched := projectionNow.Add(-10 * time.Second)
	u := &acp.ProviderUsageUpdate{
		FetchedAt: fetched.Format(time.RFC3339),
		BlockedModels: []acp.UsageBlockedModel{
			{Model: "Other-Model", Blocker: "x", RetryInSec: 9999},
			{Model: " " + strings.ToUpper(usageUpstreamID) + " ", Blocker: usageNeedleBlocker, RetryInSec: 900, RetryAt: projectionNow.Add(time.Hour).Format(time.RFC3339)},
		},
	}
	w := projectSharedUsage(u, nil, projectionRow(), projectionNow)
	if !w.Blocked || !slices.Equal(w.Blockers, []string{"model_blocked"}) {
		t.Fatalf("document: %+v", w)
	}
	if w.RetryInS != 890 { // the delay is the hub's at the read, less the ten seconds since
		t.Fatalf("retry_in_s %d, want 890", w.RetryInS)
	}
	body, _ := json.Marshal(w)
	mustNotMention(t, "the projection", string(body), "Other", "other", usageNeedleBlocker, "9999", usageUpstreamID)

	// With an account block as well, the larger delay wins and the blockers add up once.
	u.Blocked, u.Blockers, u.RetryInSec = true, []string{"rpm_exhausted", "model_blocked"}, 5000
	w = projectSharedUsage(u, nil, projectionRow(), projectionNow)
	if !slices.Equal(w.Blockers, []string{"rpm_exhausted", "model_blocked"}) || w.RetryInS != 5000 {
		t.Fatalf("combined: %+v", w)
	}
	u.RetryInSec = 30
	if w = projectSharedUsage(u, nil, projectionRow(), projectionNow); w.RetryInS != 890 {
		t.Fatalf("the larger of the two delays is not taken: %+v", w)
	}

	// An entry with only an absolute time counts from now; one that has lifted counts 0.
	u = &acp.ProviderUsageUpdate{BlockedModels: []acp.UsageBlockedModel{{Model: usageUpstreamID, RetryAt: projectionNow.Add(2 * time.Minute).Format(time.RFC3339)}}}
	if w = projectSharedUsage(u, nil, projectionRow(), projectionNow); !w.Blocked || w.RetryInS != 120 {
		t.Fatalf("an absolute-only gate: %+v", w)
	}
	u = &acp.ProviderUsageUpdate{FetchedAt: projectionNow.Add(-time.Hour).Format(time.RFC3339), BlockedModels: []acp.UsageBlockedModel{{Model: usageUpstreamID, RetryInSec: 60}}}
	if w = projectSharedUsage(u, nil, projectionRow(), projectionNow); !w.Blocked || w.RetryInS != 0 {
		t.Fatalf("a gate that lifted while the snapshot aged: %+v", w)
	}

	// A model that is not the alias's is never reported.
	u = &acp.ProviderUsageUpdate{BlockedModels: []acp.UsageBlockedModel{{Model: "somebody-else", RetryInSec: 60}}}
	if w = projectSharedUsage(u, nil, projectionRow(), projectionNow); w.Blocked || len(w.Blockers) != 0 {
		t.Fatalf("another model's gate: %+v", w)
	}
}

// ---------------------------------------------------------------------------
// The reflection tests: nothing outside the allowlist can reach the wire
// ---------------------------------------------------------------------------

func jsonNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			out = append(out, "?"+t.Field(i).Name)
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// The wire types are the allowlist: a field added to either needs this list,
// the plan's table and the projection changed together.
func TestWireUsageKeySetIsTheAllowlist(t *testing.T) {
	want := []string{"account_wide", "blocked", "blockers", "retry_in_s", "stale", "supported", "windows"}
	if got := jsonNames(reflect.TypeOf(llm.WireUsage{})); !slices.Equal(got, want) {
		t.Fatalf("WireUsage keys %v, want %v", got, want)
	}
	wantW := []string{"exhausted", "id", "label", "reset_in_s", "used_percent"}
	if got := jsonNames(reflect.TypeOf(llm.WireUsageWindow{})); !slices.Equal(got, wantW) {
		t.Fatalf("WireUsageWindow keys %v, want %v", got, wantW)
	}
}

// fillWithNeedles gives every exported field of v a value that would show on the
// wire if the projection let it through: a string naming its path, 424242 for a
// number (the few that are allowed to travel get 7), a pointer to a filled
// value, one element per slice.
func fillWithNeedles(v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("NEEDLE_" + path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strings.HasSuffix(path, "RetryInSec") || strings.HasSuffix(path, "ResetInSec") {
			v.SetInt(7)
		} else {
			v.SetInt(424242)
		}
	case reflect.Float32, reflect.Float64:
		if strings.HasSuffix(path, "UsedPercent") {
			v.SetFloat(55.5)
		} else {
			v.SetFloat(424242.5)
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillWithNeedles(v.Elem(), path)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillWithNeedles(s.Index(0), path+"[0]")
		v.Set(s)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillWithNeedles(v.Field(i), path+"."+v.Type().Field(i).Name)
			}
		}
	}
}

// Every field of acp.ProviderUsageUpdate carries a needle, including the ones
// a later change adds; none of them appears in the projection, whatever the
// field is called, and only the allowlisted keys are written.
func TestProjectSharedUsageLeaksNoFieldOfTheUpdate(t *testing.T) {
	var u acp.ProviderUsageUpdate
	fillWithNeedles(reflect.ValueOf(&u).Elem(), "U")
	// What has to be well-formed for the entries to survive their own filters.
	u.Unsupported, u.Disabled, u.Unlimited, u.Resuming, u.RefreshPending = false, false, false, false, false
	u.Windows[0].ID = "session"
	u.Windows[0].ResetsAt = "2026-10-08T13:00:00Z"
	u.BlockedModels[0].Model = usageUpstreamID
	u.BlockedModels[0].RetryAt = ""
	u.UnlimitedModels = []string{"NEEDLE_UnlimitedModel"}
	u.FetchedAt = projectionNow.Format(time.RFC3339)

	w := projectSharedUsage(&u, nil, projectionRow(), projectionNow)
	body, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	mustNotMention(t, "the projection of a snapshot with every field filled", string(body), "NEEDLE", "424242", usageUpstreamID)

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for k := range doc {
		if !slices.Contains([]string{"supported", "account_wide", "stale", "windows", "blocked", "blockers", "retry_in_s"}, k) {
			t.Fatalf("key %q is not on the allowlist: %s", k, body)
		}
	}
	var windows []map[string]json.RawMessage
	if err := json.Unmarshal(doc["windows"], &windows); err != nil || len(windows) != 1 {
		t.Fatalf("windows: %v %s", err, doc["windows"])
	}
	for k := range windows[0] {
		if !slices.Contains([]string{"id", "label", "used_percent", "reset_in_s", "exhausted"}, k) {
			t.Fatalf("window key %q is not on the allowlist: %s", k, body)
		}
	}
	// The same document with a hostile label and blocker, still nothing.
	u.Windows[0].Label = "NEEDLE_label"
	u.Blockers = []string{"NEEDLE_blocker"}
	w = projectSharedUsage(&u, nil, projectionRow(), projectionNow)
	body, _ = json.Marshal(w)
	mustNotMention(t, "the projection of hostile labels", string(body), "NEEDLE")
}
