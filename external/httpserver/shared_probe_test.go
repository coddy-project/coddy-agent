//go:build http

package httpserver

// The application probe of a vanished peer (docs/plans/remote-model-provider-probe.md; model p4-probe): a call that asked for it is
// confirmed with an id, the client pings POST /coddy/llm/calls/{id}/alive, and the call is cancelled when more than the grace passes
// without a ping. A call that did not ask is never cut by it.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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
	resp, err := sharedTestClient.Do(fx.request(http.MethodPost, "/coddy/llm/calls/"+id+"/alive", token, nil))
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

func TestProbeConfirmsOnlyACallThatAskedForIt(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	if pc.every != 10*time.Second || pc.grace != 35*time.Second {
		t.Fatalf("the plan's constants are I = 10 s and G = 35 s: %v %v", pc.every, pc.grace)
	}
	// A second call that did not ask carries no confirmation.
	resp := fx.completeWith(sharedTestSharedTok, false)
	if got := resp.Header.Get(llm.CoddyProbeHeader); got != "" {
		t.Fatalf("a call that did not ask was confirmed: %q", got)
	}
	_ = resp.Body.Close()
}

func TestProbeCutsASilentCallAfterTheGraceAndFreesItsSlot(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("the call holds no slot")
	}
	fx.clk.Advance(pc.grace - time.Second)
	if fx.srv.sharedLimit.inUse(key) != 1 {
		t.Fatal("cut before the grace")
	}
	fx.clk.Advance(2 * time.Second)
	waitFor(t, "the silent call to be cut and its slot freed", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
	// The counters say gone, and the ping of an ended call is unknown.
	waitFor(t, "the gone row", func() bool { _, ok := fx.stats().row(sharedTestAlias, sharedClassShared, sharedCountGone); return ok })
	if got := fx.ping(pc.id, sharedTestSharedTok).StatusCode; got != http.StatusNotFound {
		t.Errorf("a ping of a cut call: %d, want 404", got)
	}
}

func TestProbeAClientThatPingsIsNeverCut(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	key := sharedBearerKey(sharedTestSharedTok)
	for i := 0; i < 8; i++ { // 80 s, more than twice the grace
		fx.clk.Advance(pc.every)
		resp := fx.ping(pc.id, sharedTestSharedTok)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("ping %d: %d %s", i, resp.StatusCode, bodyString(t, resp))
		}
		_ = resp.Body.Close()
		if fx.srv.sharedLimit.inUse(key) != 1 {
			t.Fatalf("a client that pings was cut at ping %d", i)
		}
	}
	// It stops pinging: the grace runs from its last ping.
	fx.clk.Advance(pc.grace + time.Second)
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

func TestProbePingRefusals(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	pc := fx.openProbed(t)
	for name, c := range map[string]struct {
		id, token string
		want      int
	}{
		"unknown id":         {strings.Repeat("0", 32), sharedTestSharedTok, http.StatusNotFound},
		"short id":           {"abc", sharedTestSharedTok, http.StatusNotFound},
		"upper-case id":      {strings.ToUpper(pc.id), sharedTestSharedTok, http.StatusNotFound},
		"another credential": {pc.id, sharedTestMainToken, http.StatusNotFound},
		"no credential":      {pc.id, "", http.StatusUnauthorized},
		"a wrong token":      {pc.id, "nope", http.StatusUnauthorized},
	} {
		resp := fx.ping(pc.id, c.token)
		if c.id != pc.id {
			resp = fx.ping(c.id, c.token)
		}
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", name, resp.StatusCode, c.want)
		}
		if c.want == http.StatusNotFound {
			var e llm.WireError
			if err := json.Unmarshal([]byte(bodyString(t, resp)), &e); err != nil || e.Kind != llm.WireKindInvalid || e.Code != llm.WireCodeUnknownCall {
				t.Errorf("%s: %+v %v", name, e, err)
			}
		}
	}
	// The refused pings did not keep the call alive: the grace still cuts it.
	key := sharedBearerKey(sharedTestSharedTok)
	fx.clk.Advance(pc.grace + time.Second)
	waitFor(t, "the cut", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
}

func TestProbeTheCompletionAndTheGuardReleaseTheSlotOnce(t *testing.T) {
	fx := newSharedFixture(t, withFakeClock())
	release := make(chan struct{})
	started := make(chan struct{}, 16)
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
	rd := newSSEReader(t, resp)
	<-started
	key := sharedBearerKey(sharedTestSharedTok)
	// The call completes and the grace runs out at the same moment.
	close(release)
	fx.clk.Advance(36 * time.Second)
	// Either the final frame or the cut ends the stream, whichever comes first: drain until it closes.
	for {
		if _, ok := rd.next(5 * time.Second); !ok {
			break
		}
	}
	waitFor(t, "the slot to be free", func() bool { return fx.srv.sharedLimit.inUse(key) == 0 })
	// Nothing went negative: a fresh call is admitted up to the limit again.
	for i := 0; i < fx.cfg.HTTPServer.EffectiveSharedMaxStreams(); i++ {
		r := fx.complete(wireReq(sharedTestAlias))
		if r.StatusCode != http.StatusOK {
			t.Fatalf("call %d after the race: %d", i, r.StatusCode)
		}
		newSSEReader(t, r).all()
	}
}

func TestProbeRouteIsADocumentedSharedRoute(t *testing.T) {
	if !isSharedLLMPattern(sharedAlivePattern) {
		t.Fatal("the gate must admit the ping for the classes of the three shared routes")
	}
	if sharedAlivePattern != "POST /coddy/llm/calls/{id}/alive" {
		t.Fatalf("%q", sharedAlivePattern)
	}
}
