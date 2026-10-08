//go:build swarm

package swarm

// The relay's own limits of a scoped client (docs/plans/remote-model-provider-phase3.md, 5.3; D3 closed by
// docs/plans/remote-model-provider-models/p3-d3-rate-windows.md): a slot count and a window of calls per minute per entry, a
// refusal that costs the node nothing, a refund of the window token when the node answers busy.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/shareguard"
)

const relayCompletions = "/swarm/nodes/nas02/coddy/llm/completions"

type fakeTime struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeTime) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeTime) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// gateNode is a node whose completions block until released, or answer a scripted status.
type gateNode struct {
	ts      *httptest.Server
	hold    chan struct{}
	entered chan struct{}
	status  atomic.Int32 // 0: hold then 200; other: answer it at once
	calls   atomic.Int32
}

func newGateNode(t *testing.T) *gateNode {
	t.Helper()
	g := &gateNode{hold: make(chan struct{}), entered: make(chan struct{}, 16)}
	g.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.calls.Add(1)
		if r.URL.Path != "/coddy/llm/completions" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if st := int(g.status.Load()); st != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(st)
			_, _ = io.WriteString(w, `{"kind":"busy","emitted":false}`)
			return
		}
		g.entered <- struct{}{}
		select {
		case <-g.hold:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "done")
	}))
	t.Cleanup(g.ts.Close)
	return g
}

func limitRelay(t *testing.T, g *gateNode, clock *fakeTime, clients ...config.SwarmClient) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.Clients = clients
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if clock != nil {
		srv.limits = newClientLimits(clock.Now)
	}
	if _, err := srv.registry.Register(registerRequestFor("nas02", g.ts.URL)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func post(t *testing.T, url, token string) (*http.Response, string) {
	t.Helper()
	return do(t, http.MethodPost, url, token)
}

func limited(name, token string, streams, perMinute, burst int) config.SwarmClient {
	return config.SwarmClient{Name: name, Token: token, Scope: config.ScopeSharedModels, Nodes: []string{"nas02"},
		MaxStreams: streams, RatePerMinute: perMinute, RateBurst: burst}
}

func decodeWire(t *testing.T, body string) llm.WireError {
	t.Helper()
	var e llm.WireError
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not the error object: %v: %s", err, body)
	}
	return e
}

func TestAClientIsHeldToItsOwnSlots(t *testing.T) {
	g := newGateNode(t)
	srv, ts := limitRelay(t, g, nil, limited("acme", "acme-secret", 1, 0, 0), limited("beta", "beta-secret", 0, 0, 0))

	first := make(chan string, 1)
	go func() { _, b := post(t, ts.URL+relayCompletions, "acme-secret"); first <- b }()
	<-g.entered

	res, body := post(t, ts.URL+relayCompletions, "acme-secret")
	e := decodeWire(t, body)
	if res.StatusCode != http.StatusTooManyRequests || e.Kind != llm.WireKindBusy || e.Code != llm.WireCodeClientStreams || res.Header.Get("Retry-After") != "1" || e.RetryAfterS != 1 {
		t.Fatalf("the second call of a client at its limit: %d %+v Retry-After %q", res.StatusCode, e, res.Header.Get("Retry-After"))
	}
	if res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", res.Header)
	}
	if g.calls.Load() != 1 {
		t.Errorf("the refusal reached the node: %d calls", g.calls.Load())
	}
	// Another client of the same relay is not held by it.
	other := make(chan string, 1)
	go func() { _, b := post(t, ts.URL+relayCompletions, "beta-secret"); other <- b }()
	<-g.entered
	// The listing of the same client is not a call and is not limited.
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/llm/models", "acme-secret"); res.StatusCode != http.StatusOK {
		t.Errorf("a listing was limited: %d", res.StatusCode)
	}
	close(g.hold)
	<-first
	<-other
	waitUntil(t, "the slots to come back", func() bool { return srv.limits.inUse("acme") == 0 && srv.limits.inUse("beta") == 0 })
}

func TestAClientIsHeldToItsWindow(t *testing.T) {
	g := newGateNode(t)
	g.status.Store(0)
	close(g.hold) // the node answers at once
	clock := &fakeTime{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	_, ts := limitRelay(t, g, clock, limited("acme", "acme-secret", 0, 60, 2))
	for i := 0; i < 2; i++ {
		if res, _ := post(t, ts.URL+relayCompletions, "acme-secret"); res.StatusCode != http.StatusOK {
			t.Fatalf("call %d of the burst: %d", i+1, res.StatusCode)
		}
	}
	before := g.calls.Load()
	res, body := post(t, ts.URL+relayCompletions, "acme-secret")
	e := decodeWire(t, body)
	if res.StatusCode != http.StatusTooManyRequests || e.Code != llm.WireCodeClientRate || e.Kind != llm.WireKindBusy {
		t.Fatalf("a call past the window: %d %+v", res.StatusCode, e)
	}
	if got := res.Header.Get("Retry-After"); got != "1" || e.RetryAfterS != 1 {
		t.Errorf("Retry-After %q retry_after_s %v, want 1", got, e.RetryAfterS)
	}
	if g.calls.Load() != before {
		t.Error("the window refusal reached the node")
	}
	clock.Advance(time.Second)
	if res, _ := post(t, ts.URL+relayCompletions, "acme-secret"); res.StatusCode != http.StatusOK {
		t.Errorf("after the next token: %d", res.StatusCode)
	}
}

// A client that waits out the node's busy does not drain its relay window by waiting.
func TestTheRelayRefundsItsTokenWhenTheNodeAnswersBusy(t *testing.T) {
	g := newGateNode(t)
	g.status.Store(http.StatusTooManyRequests)
	clock := &fakeTime{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	srv, ts := limitRelay(t, g, clock, limited("acme", "acme-secret", 1, 60, 1))
	for i := 0; i < 8; i++ {
		res, _ := post(t, ts.URL+relayCompletions, "acme-secret")
		if res.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: %d", i, res.StatusCode)
		}
		// The node answered; if the relay's own window had refused, the node would not have been asked.
		if int(g.calls.Load()) != i+1 {
			t.Fatalf("attempt %d: the node saw %d calls: the relay's window refused a call whose token should have come back", i, g.calls.Load())
		}
	}
	waitUntil(t, "the slot to come back", func() bool { return srv.limits.inUse("acme") == 0 })
	// Without the node's busy the same client spends its token.
	g.status.Store(0)
	close(g.hold)
	if res, _ := post(t, ts.URL+relayCompletions, "acme-secret"); res.StatusCode != http.StatusOK {
		t.Fatalf("an admitted call: %d", res.StatusCode)
	}
	if res, body := post(t, ts.URL+relayCompletions, "acme-secret"); decodeWire(t, body).Code != llm.WireCodeClientRate || res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the window after an admitted call: %d %s", res.StatusCode, body)
	}
}

// The slot is given back on every exit: a final answer, an unreachable node, a client that went away.
func TestTheSlotIsGivenBackOnEveryExit(t *testing.T) {
	g := newGateNode(t)
	srv, ts := limitRelay(t, g, nil, limited("acme", "acme-secret", 1, 0, 0))

	// a client that goes away while the node holds the call
	req, _ := http.NewRequest(http.MethodPost, ts.URL+relayCompletions, nil)
	req.Header.Set("Authorization", "Bearer acme-secret")
	ctx, cancel := contextWithCancel()
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() { _, _ = http.DefaultClient.Do(req); close(done) }()
	<-g.entered
	cancel()
	<-done
	waitUntil(t, "the slot after a client that left", func() bool { return srv.limits.inUse("acme") == 0 })

	// an unreachable node
	g.ts.Close()
	if res, _ := post(t, ts.URL+relayCompletions, "acme-secret"); res.StatusCode < 500 {
		t.Errorf("an unreachable node: %d", res.StatusCode)
	}
	waitUntil(t, "the slot after an unreachable node", func() bool { return srv.limits.inUse("acme") == 0 })
}

// The relay writes the same flat object as the node without importing the node's package; the key set is pinned to the wire's.
func TestTheRelayRefusalHasTheKeysOfTheWireError(t *testing.T) {
	typ := reflect.TypeOf(llm.WireError{})
	wire := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		wire[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	raw, _ := json.Marshal(busyRefusal{Kind: "busy", Code: "client_streams", Message: "m", RetryAfterS: 1, Status: 429})
	var got map[string]json.RawMessage
	_ = json.Unmarshal(raw, &got)
	var keys []string
	for k := range got {
		keys = append(keys, k)
		if !wire[k] {
			t.Errorf("key %q is not a key of the wire error", k)
		}
	}
	sort.Strings(keys)
	for _, must := range []string{"emitted", "kind"} {
		if _, ok := got[must]; !ok {
			t.Errorf("the refusal lacks %q: %v", must, keys)
		}
	}
}

func TestLimitsAreOffUntilConfigured(t *testing.T) {
	g := newGateNode(t)
	close(g.hold)
	_, ts := limitRelay(t, g, nil, limited("acme", "acme-secret", 0, 0, 0))
	for i := 0; i < 6; i++ {
		if res, _ := post(t, ts.URL+relayCompletions, "acme-secret"); res.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d", i, res.StatusCode)
		}
	}
	// The full class is not limited at the relay.
	for i := 0; i < 6; i++ {
		if res, _ := post(t, ts.URL+relayCompletions, fullToken); res.StatusCode != http.StatusOK {
			t.Fatalf("full call %d: %d", i, res.StatusCode)
		}
	}
}

var _ = shareguard.Key{}
