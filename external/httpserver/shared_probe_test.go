//go:build http

package httpserver

// The application probe of a vanished peer (docs/plans/remote-model-provider-probe.md; model p4-probe): a call that asked for it is
// confirmed with an id, the client pings POST /coddy/llm/calls/{id}/alive, and the call is cancelled when more than the grace passes
// without a ping. A call that did not ask is never cut by it.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

var probeHeaderRE = regexp.MustCompile(`^id=([0-9a-f]{32}); every_ms=(\d+); grace_ms=(\d+)$`)

// probedCall is a completion that is held open by a provider that waits for its context.
type probedCall struct {
	resp    *http.Response
	rd      *sseReader
	id      string
	every   time.Duration
	grace   time.Duration
	started chan struct{}
	done    chan struct{}
}

// holdCalls makes the provider hold every call until its context ends or the test does, so a call that nothing cuts still ends when
// the test is over and does not hold the server's Close.
func (fx *sharedFixture) holdCalls() (started chan struct{}) {
	started = make(chan struct{}, 8)
	release := make(chan struct{})
	fx.t.Cleanup(func() { close(release) })
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return &llm.Response{Content: "released"}, nil
		}
	}
	return started
}

func (fx *sharedFixture) completeWith(token string, optIn bool) *http.Response {
	fx.t.Helper()
	raw, _ := json.Marshal(wireReq(sharedTestAlias))
	req := fx.request(http.MethodPost, llm.CoddyCompletionsPath, token, bytes.NewReader(raw))
	if optIn {
		req.Header.Set(llm.CoddyProbeHeader, "1")
	}
	resp, err := sharedTestClient.Do(req)
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

func (fx *sharedFixture) openProbed(t *testing.T) *probedCall {
	t.Helper()
	started := fx.holdCalls()
	resp := fx.completeWith(sharedTestSharedTok, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, bodyString(t, resp))
	}
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	if m == nil {
		t.Fatalf("no confirmation: %q", resp.Header.Get(llm.CoddyProbeHeader))
	}
	every, _ := strconv.Atoi(m[2])
	grace, _ := strconv.Atoi(m[3])
	pc := &probedCall{resp: resp, rd: newSSEReader(t, resp), id: m[1], every: time.Duration(every) * time.Millisecond,
		grace: time.Duration(grace) * time.Millisecond, started: started, done: make(chan struct{})}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider was never called")
	}
	return pc
}

func (fx *sharedFixture) ping(id, token string) *http.Response {
	fx.t.Helper()
	req := fx.request(http.MethodPost, llm.CoddyAlivePath, token, nil)
	if id != "" {
		req.Header.Set(llm.CoddyProbeIDHeader, id)
	}
	resp, err := sharedTestClient.Do(req)
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

// pingOK sends a ping that has to be accepted and closes its response.
func (fx *sharedFixture) pingOK(t *testing.T, id, token string) {
	t.Helper()
	resp := fx.ping(id, token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ping: %d %s", resp.StatusCode, bodyString(t, resp))
	}
}

// slack is the second the guard adds to the grace (the model's tie rule).
const probeSlack = time.Second

func TestProbeConfirmsOnlyACallThatAskedForIt(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	if pc.every != 10*time.Second || pc.grace != 35*time.Second {
		t.Fatalf("the plan's constants are I = 10 s and G = 35 s: %v %v", pc.every, pc.grace)
	}
	// The id is good at once, with no wait: a client pings the moment it reads the header.
	fx.pingOK(t, pc.id, sharedTestSharedTok)
	// A second call that did not ask carries no confirmation.
	resp := fx.completeWith(sharedTestSharedTok, false)
	if got := resp.Header.Get(llm.CoddyProbeHeader); got != "" {
		t.Fatalf("a call that did not ask was confirmed: %q", got)
	}
	_ = resp.Body.Close()
}

func TestProbeARefusedCallIsNotConfirmed(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	fx.openProbed(t) // takes the only slot
	resp := fx.completeWith(sharedTestSharedTok, true)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get(llm.CoddyProbeHeader) != "" {
		t.Fatalf("a busy refusal: %d, header %q", resp.StatusCode, resp.Header.Get(llm.CoddyProbeHeader))
	}
}

// Fail-open (model p4-probe, BROKEN): a confirmed call whose client never pings has no guard, so a path that carries no ping cuts no live client.
func TestProbeNoPingNoGuard(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	// Six graces, 210 s: under the stall guard's 300 s, which would end the call for another reason.
	for i := 0; i < 6; i++ {
		fx.clk.Advance(pc.grace)
	}
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("a confirmed call that was never pinged was cut: the guard is armed by the first ping")
	}
}

func TestProbeCutsASilentCallAfterItsLastPingAndTellsWhy(t *testing.T) {
	var logs bytes.Buffer
	fx := newSharedFixture(t, withFakeClock(), withSharedLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	fx.pingOK(t, pc.id, sharedTestSharedTok) // arms the guard
	fx.clk.Advance(pc.grace)                 // exactly G of silence: not yet (the tie goes to the ping)
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("cut at G: the guard waits one more second")
	}
	fx.clk.Advance(probeSlack)
	waitFor(t, "the silent call to be cut and its slot freed", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
	// The terminal frame is not transient, and the call is counted and logged as gone with the cause.
	var terminal string
	for {
		ev, ok := pc.rd.next(5 * time.Second)
		if !ok {
			break
		}
		if ev.typ == "error" {
			terminal = string(ev.raw)
		}
	}
	if !strings.Contains(terminal, `"kind":"invalid"`) || !strings.Contains(terminal, llm.WireCodeProbeLapsed) {
		t.Errorf("terminal frame %q", terminal)
	}
	waitFor(t, "the gone row", func() bool { _, ok := fx.stats().row(sharedTestAlias, sharedClassShared, sharedCountGone); return ok })
	if !strings.Contains(logs.String(), "cause=probe") || !strings.Contains(logs.String(), "client_gone") {
		t.Errorf("the log does not say why: %s", logs.String())
	}
	if resp := fx.ping(pc.id, sharedTestSharedTok); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a ping of a cut call: %d, want 404", resp.StatusCode)
	}
}

// The tie: a ping that lands exactly when the grace ends wins; one a second later finds the call cut.
func TestProbeThePingOnTheTickTheGraceEndsWins(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	fx.pingOK(t, pc.id, sharedTestSharedTok)
	fx.clk.Advance(pc.grace)
	fx.pingOK(t, pc.id, sharedTestSharedTok) // at G: accepted, and re-arms
	fx.clk.Advance(pc.grace)
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("the ping at G did not re-arm the guard")
	}
	fx.clk.Advance(probeSlack + time.Second)
	waitFor(t, "the cut", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
	if resp := fx.ping(pc.id, sharedTestSharedTok); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a ping after the cut: %d", resp.StatusCode)
	}
}

func TestProbeAClientThatPingsIsNeverCut(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	fx.pingOK(t, pc.id, sharedTestSharedTok)
	for i := 0; i < 8; i++ { // 80 s, more than twice the grace
		fx.clk.Advance(pc.every)
		fx.pingOK(t, pc.id, sharedTestSharedTok)
		if fx.srv.sharedLimit.inUse(key) != 1 {
			t.Fatalf("a client that pings was cut at ping %d", i)
		}
	}
	// It stops pinging: the grace runs from its last ping.
	fx.clk.Advance(pc.grace + probeSlack + time.Second)
	waitFor(t, "the cut after the last ping", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
}

func TestProbeACallThatDidNotAskIsNeverCut(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	started := fx.holdCalls()
	resp := fx.completeWith(sharedTestSharedTok, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d", resp.StatusCode)
	}
	rd := newSSEReader(t, resp)
	<-started
	key := sharedBearerKey(sharedTestSharedTok)
	for i := 0; i < 6; i++ {
		fx.clk.Advance(30 * time.Second)
	}
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("a call that did not ask was cut by the probe")
	}
	_ = rd
}

// A ping the node refuses must not keep the call alive: the guard runs from the last ACCEPTED ping. The refused pings come midway, so a
// handler that refreshed the guard before it refused would postpone the cut past the point asserted at the end.
func TestProbeRefusedPingsDoNotRefreshTheGuard(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock(), withSharedConfig(func(c *config.Config) {
		c.HTTPServer.SharedModels.Tokens = []string{sharedTestSharedTok, "second-shared-token"}
	}))
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	fx.pingOK(t, pc.id, sharedTestSharedTok)
	fx.clk.Advance(20 * time.Second)
	for name, c := range map[string]struct {
		id, token string
		want      int
	}{
		"unknown id":                      {strings.Repeat("0", 32), sharedTestSharedTok, http.StatusNotFound},
		"short id":                        {"abc", sharedTestSharedTok, http.StatusNotFound},
		"upper-case id":                   {strings.ToUpper(pc.id), sharedTestSharedTok, http.StatusNotFound},
		"no id":                           {"", sharedTestSharedTok, http.StatusNotFound},
		"another token of the same class": {pc.id, "second-shared-token", http.StatusNotFound},
		"the main token":                  {pc.id, sharedTestMainToken, http.StatusNotFound},
		"no credential":                   {pc.id, "", http.StatusUnauthorized},
		"a wrong token":                   {pc.id, "nope", http.StatusUnauthorized},
	} {
		resp := fx.ping(c.id, c.token)
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", name, resp.StatusCode, c.want)
		}
		if c.want == http.StatusNotFound {
			var e llm.WireError
			if err := json.Unmarshal([]byte(bodyString(t, resp)), &e); err != nil || e.Kind != llm.WireKindInvalid || e.Code != llm.WireCodeUnknownCall {
				t.Errorf("%s: %+v %v", name, e, err)
			}
		} else {
			_ = resp.Body.Close()
		}
	}
	// 36 s after the last accepted ping (G plus the second): cut, though refused pings came at 20 s.
	fx.clk.Advance(16 * time.Second)
	waitFor(t, "the cut on schedule", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
}

// A ping is not a call: it takes no slot, spends no window token, writes no call log line and leaves no row of the counters.
func TestProbeAPingIsNotACall(t *testing.T) {
	var logs bytes.Buffer
	fx := newSharedFixture(t, withFakeClock(), withSharedLogger(slog.New(slog.NewTextHandler(&logs, nil))),
		withSharedConfig(func(c *config.Config) {
			c.HTTPServer.SharedModels.MaxStreams = 1
			c.HTTPServer.SharedModels.RatePerMinute = 1
			c.HTTPServer.SharedModels.RateBurst = 1
		}))
	pc := fx.openProbed(t)
	logged := strings.Count(logs.String(), "shared model call")
	rows := len(fx.stats().Rows)
	for i := 0; i < 5; i++ {
		fx.pingOK(t, pc.id, sharedTestSharedTok) // the slot is full and the window spent: a ping still passes
	}
	if got := strings.Count(logs.String(), "shared model call"); got != logged {
		t.Errorf("pings wrote %d call log lines", got-logged)
	}
	if got := len(fx.stats().Rows); got != rows {
		t.Errorf("pings left rows in the counters: %d then %d", rows, got)
	}
	if got := fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)); got != 1 {
		t.Errorf("slots in use: %d, want the call's own one", got)
	}
}

func TestProbeAMainTokenCallIsPingedWithTheMainTokenOnly(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	started := fx.holdCalls()
	resp := fx.completeWith(sharedTestMainToken, true)
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	if m == nil {
		t.Fatalf("no confirmation for a main-token call")
	}
	_ = newSSEReader(t, resp)
	<-started
	if got := fx.ping(m[1], sharedTestSharedTok).StatusCode; got != http.StatusNotFound {
		t.Errorf("a shared token pinging a main-token call: %d, want 404", got)
	}
	fx.pingOK(t, m[1], sharedTestMainToken)
}

func TestProbeAnonymousCallers(t *testing.T) {
	// No credential of any class and no allow_insecure: the shared routes are closed, the ping too.
	closed := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.AuthToken = ""
		c.HTTPServer.SharedModels.Tokens = nil
	}))
	if got := closed.ping(strings.Repeat("a", 32), "").StatusCode; got != http.StatusForbidden {
		t.Errorf("a node with no credential: %d, want 403", got)
	}
	// An open node on purpose: an anonymous call is pinged by an anonymous caller.
	open := newSharedFixture(t, withFakeClock(), withSharedConfig(func(c *config.Config) {
		c.HTTPServer.AuthToken = ""
		c.HTTPServer.SharedModels.Tokens = nil
		c.HTTPServer.AllowInsecure = true
	}))
	started := open.holdCalls()
	resp := open.completeWith("", true)
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	if m == nil {
		t.Fatalf("no confirmation on an open node: %d", resp.StatusCode)
	}
	_ = newSSEReader(t, resp)
	<-started
	open.pingOK(t, m[1], "")
}

func TestProbeAPingAfterANormalCompletionIsUnknown(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &llm.Response{Content: "done"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	resp := fx.completeWith(sharedTestSharedTok, true)
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	rd := newSSEReader(t, resp)
	<-started
	fx.pingOK(t, m[1], sharedTestSharedTok)
	close(release)
	rd.all()
	waitFor(t, "the id to be forgotten", func() bool { return fx.srv.sharedProbes.lookup(m[1]) == nil })
	if got := fx.ping(m[1], sharedTestSharedTok).StatusCode; got != http.StatusNotFound {
		t.Errorf("a ping of a finished call: %d, want 404", got)
	}
}

// The guard and the completion fall together: the slot of the call is released once, and the slot of another call of the same
// credential, which is held meanwhile, is not taken by a second release.
func TestProbeTheCompletionAndTheGuardReleaseTheSlotOnce(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	release := make(chan struct{})
	started := make(chan struct{}, 16)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	var first atomic.Bool
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		started <- struct{}{}
		if first.CompareAndSwap(false, true) {
			select {
			case <-release:
				return &llm.Response{Content: "done"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select { // the second call is held until the test ends
		case <-hold:
			return &llm.Response{Content: "held"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	resp := fx.completeWith(sharedTestSharedTok, true)
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	rd := newSSEReader(t, resp)
	<-started
	fx.pingOK(t, m[1], sharedTestSharedTok)
	other := fx.complete(wireReq(sharedTestAlias))
	_ = newSSEReader(t, other)
	<-started
	key := sharedBearerKey(sharedTestSharedTok)
	if fx.srv.sharedLimit.inUse(key) != 2 {
		t.Fatalf("two calls hold %d slots", fx.srv.sharedLimit.inUse(key))
	}
	// The first call completes and its grace runs out at the same moment.
	close(release)
	fx.clk.Advance(36 * time.Second)
	for { // either the final frame or the cut ends the stream, whichever comes first
		if _, ok := rd.next(5 * time.Second); !ok {
			break
		}
	}
	waitFor(t, "the first slot to be free", func() bool { return fx.srv.sharedLimit.inUse(key) == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := fx.srv.sharedLimit.inUse(key); got != 1 {
		t.Fatalf("the other call's slot was taken by a second release: %d in use, want 1", got)
	}
}

func TestProbeRouteIsADocumentedSharedRoute(t *testing.T) {
	if !isSharedLLMPattern(sharedAlivePattern) {
		t.Fatal("the gate must admit the ping for the classes of the three shared routes")
	}
	if sharedAlivePattern != "POST /coddy/llm/alive" {
		t.Fatalf("%q", sharedAlivePattern)
	}
}
