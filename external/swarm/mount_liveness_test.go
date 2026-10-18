//go:build swarm

package swarm

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// h2LogLine is the one line the mount logs per connection of an HTTP/2 client
// it cannot probe; the documentation names it.
const h2LogLine = "shared call over HTTP/2: a vanished client is not detected before the call ends"

// wantOption checks the option the relay's client connection carried when the
// node started a call. A platform without the option reports it as an error,
// and then there is nothing to compare.
func wantOption(t *testing.T, what string, c probeCall, want time.Duration) {
	t.Helper()
	if c.relayOptionErr != nil {
		if errors.Is(c.relayOptionErr, platform.ErrUserTimeoutUnsupported) {
			return
		}
		t.Fatalf("%s: reading the relay's option: %v", what, c.relayOptionErr)
	}
	if c.relayOption != want {
		t.Fatalf("%s: the relay's client connection carried user timeout %v, want %v", what, c.relayOption, want)
	}
}

// The mount keeps its copy of the route in step with the client's.
func TestMountLivenessRouteIsTheClientsRoute(t *testing.T) {
	if sharedCompletionsRoute != llm.CoddyCompletionsPath {
		t.Fatalf("mount route %q differs from the client's %q", sharedCompletionsRoute, llm.CoddyCompletionsPath)
	}
	if completionsPath != llm.CoddyCompletionsPath {
		t.Fatalf("test route %q differs from the client's %q", completionsPath, llm.CoddyCompletionsPath)
	}
}

func TestMountLivenessRouteMatcher(t *testing.T) {
	tests := []struct {
		method, rest string
		want         bool
	}{
		{http.MethodPost, "/coddy/llm/completions", true},
		{http.MethodPost, "/swarm/nodes/b/coddy/llm/completions", true},
		{http.MethodPost, "/swarm/nodes/b/swarm/nodes/c/coddy/llm/completions", true},
		// The relay hands the escaped remainder; the node routes by the decoded path.
		{http.MethodPost, "/coddy/llm/%63ompletions", true},
		{http.MethodPost, "/swarm/nodes/b/coddy/llm/%63ompletions", true},
		{http.MethodPost, "/swarm/nodes/b/swarm/nodes/c/coddy/llm/%63ompletions", true},
		{http.MethodGet, "/coddy/llm/%63ompletions", false},
		{http.MethodPost, "/coddy/llm/%6dodels", false},
		{http.MethodPost, "/coddy/llm/%63ompletions/", false},
		{http.MethodPost, "/coddy/llm/%zzompletions", false},
		{http.MethodGet, "/coddy/llm/completions", false},
		{http.MethodPut, "/coddy/llm/completions", false},
		{http.MethodPost, "/coddy/llm/completions/", false},
		{http.MethodPost, "/coddy/llm/completions/x", false},
		{http.MethodPost, "/coddy/llm/models", false},
		{http.MethodPost, "/v1/responses", false},
		{http.MethodPost, "/swarm/nodes/b", false},
		{http.MethodPost, "/swarm/nodes/b/", false},
		{http.MethodPost, "/swarm/nodes/b/v1/responses", false},
		{http.MethodPost, "/swarm/nodes/coddy/llm/completions", false}, // "coddy" is a node name here
		{http.MethodPost, "/", false},
		{http.MethodPost, "", false},
	}
	for _, tt := range tests {
		if got := isSharedCompletions(tt.method, tt.rest); got != tt.want {
			t.Errorf("isSharedCompletions(%s, %q) = %v, want %v", tt.method, tt.rest, got, tt.want)
		}
	}
}

// The option is U = B - H for the call and gone after it, whichever way the
// call ends: a kept-alive connection must start its next request at the system
// default. Scaled to B = 13 s and H = 3 s so the seam is exercised as well.
func TestMountLivenessOptionIsSetForTheCallAndRestoredOnEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name string
		tls  bool
	}{{"plain relay", false}, {"TLS relay", true}} {
		t.Run(tc.name, func(t *testing.T) {
			defer httpx.ScaleLiveness(13*time.Second, 3*time.Second)()
			stand := newLiveStand(t, standOptions{tls: tc.tls})
			stand.requireUserTimeout()
			stand.node.releaseAll() // every call ends with its final frame at once
			client := stand.h1Client(nil)

			// 1. A call that runs to its final frame.
			ch := stand.start(client, http.MethodPost, completionsPath, nil)
			wantOption(t, "completions", stand.awaitStarted(), 10*time.Second)
			if r := stand.result(ch); r.err != nil || r.status != http.StatusOK || r.proto != 1 {
				t.Fatalf("completions: %+v", r)
			}
			// 2. The next request of the same connection starts at the default.
			ch = stand.start(client, http.MethodGet, "/coddy/llm/models", nil)
			wantOption(t, "next request after a final frame", stand.awaitStarted(), 0)
			_ = stand.result(ch)

			// 3. A call the node answers with an error status.
			ch = stand.start(client, http.MethodPost, completionsPath, map[string]string{"X-Probe-Fail": "1"})
			wantOption(t, "completions answered with an error", stand.awaitStarted(), 10*time.Second)
			if r := stand.result(ch); r.err != nil || r.status != http.StatusInternalServerError {
				t.Fatalf("completions with an error answer: %+v", r)
			}
			ch = stand.start(client, http.MethodGet, "/coddy/llm/models", nil)
			wantOption(t, "next request after an error answer", stand.awaitStarted(), 0)
			_ = stand.result(ch)

			// 4. A node the relay cannot reach: the relay's own hop error.
			stand.nodeServer.Close()
			ch = stand.start(client, http.MethodPost, completionsPath, nil)
			if r := stand.result(ch); r.err != nil || r.status != http.StatusBadGateway {
				t.Fatalf("completions to an unreachable node: %+v", r)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				got, err := stand.relayOption()
				if err != nil {
					t.Fatal(err)
				}
				if got == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("after the relay's own error the client connection still carries %v", got)
				}
				time.Sleep(5 * time.Millisecond)
			}

			if n := stand.connCount(); n != 1 {
				t.Fatalf("the calls used %d connections, want one kept-alive connection", n)
			}
		})
	}
}

// With nothing scaled the relay works to the decided bound: 45 s - 15 s.
func TestMountLivenessUsesTheDecidedBoundByDefault(t *testing.T) {
	stand := newLiveStand(t, standOptions{})
	stand.requireUserTimeout()
	stand.node.releaseAll()
	ch := stand.start(stand.h1Client(nil), http.MethodPost, completionsPath, nil)
	wantOption(t, "completions", stand.awaitStarted(), 30*time.Second)
	_ = stand.result(ch)
}

// Nothing but the completions route is probed: a stream of the web UI or a
// listing read through the same relay keeps the system default.
func TestMountLivenessOnlyTheCompletionsRouteSetsTheOption(t *testing.T) {
	stand := newLiveStand(t, standOptions{})
	stand.requireUserTimeout()
	stand.node.releaseAll()
	client := stand.h1Client(nil)

	tests := []struct {
		method, path string
		want         time.Duration
	}{
		{http.MethodPost, completionsPath, 30 * time.Second},
		{http.MethodPost, completionsPath + "?x=1", 30 * time.Second},
		{http.MethodGet, completionsPath, 0},
		{http.MethodPost, completionsPath + "/", 0},
		{http.MethodPost, "/v1/responses", 0},
		{http.MethodGet, "/coddy/llm/models", 0},
		{http.MethodGet, "/coddy/llm/models/coder/usage", 0},
		{http.MethodPost, "/coddy/sessions", 0},
	}
	for _, tt := range tests {
		ch := stand.start(client, tt.method, tt.path, nil)
		wantOption(t, tt.method+" "+tt.path, stand.awaitStarted(), tt.want)
		_ = stand.result(ch)
	}
	if n := stand.connCount(); n != 1 {
		t.Fatalf("the calls used %d connections, want one", n)
	}
}

// A relay in front of a relay bounds its own client too: its mount sees the
// completions route behind the hop it forwards.
func TestMountLivenessCoversTheFirstRelayOfAChain(t *testing.T) {
	inner := newLiveStand(t, standOptions{}) // the child relay B with the node
	inner.node.releaseAll()

	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = "outer-secret"
	outer, err := New(cfg, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Close()
	var mu sync.Mutex
	var outerConns []net.Conn
	front := httptest.NewUnstartedServer(outer.Handler())
	front.Config.ConnContext = httpx.ConnContext
	front.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st == http.StateNew {
			mu.Lock()
			outerConns = append(outerConns, c)
			mu.Unlock()
		}
	}
	front.Start()
	defer front.Close()
	if _, err := outer.registry.Register(swarmdto.RegisterRequest{
		Name: "child", Kind: swarmdto.KindRelay, Transport: swarmdto.TransportDirect,
		AdvertiseURL: inner.relay.URL, InstanceUUID: "uuid-child", Token: standClientToken, Version: "test",
	}); err != nil {
		t.Fatal(err)
	}
	// What the node reports is the first relay's client connection.
	inner.node.observe = func() (time.Duration, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(outerConns) == 0 {
			return 0, fmt.Errorf("the first relay accepted no connection")
		}
		return platform.TCPUserTimeout(tcpUnder(outerConns[len(outerConns)-1]))
	}
	inner.requireUserTimeout()

	req, err := http.NewRequest(http.MethodPost,
		front.URL+swarmdto.MountPath+"child"+swarmdto.MountPath+standNodeName+completionsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer outer-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d through two relays", res.StatusCode)
	}
	wantOption(t, "client of the first relay of a chain", inner.awaitStarted(), 30*time.Second)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// An HTTP/2 client of a TLS relay is served, with no option and one log line
// per connection: the documented residual.
func TestMountLivenessAnHTTP2ClientIsServedUnprobedAndLoggedOncePerConnection(t *testing.T) {
	stand := newLiveStand(t, standOptions{tls: true})
	stand.node.releaseAll()

	client := stand.h2Client(nil)
	for i := 0; i < 2; i++ {
		ch := stand.start(client, http.MethodPost, completionsPath, nil)
		wantOption(t, fmt.Sprintf("HTTP/2 call %d", i+1), stand.awaitStarted(), 0)
		if r := stand.result(ch); r.err != nil || r.status != http.StatusOK || r.proto != 2 {
			t.Fatalf("HTTP/2 call %d: %+v", i+1, r)
		}
	}
	if n := stand.log.count(h2LogLine); n != 1 {
		t.Fatalf("two calls on one HTTP/2 connection logged the residual %d times, want once", n)
	}

	// A second connection is a second line.
	other := stand.h2Client(nil)
	ch := stand.start(other, http.MethodPost, completionsPath, nil)
	wantOption(t, "HTTP/2 call on a new connection", stand.awaitStarted(), 0)
	if r := stand.result(ch); r.err != nil || r.proto != 2 {
		t.Fatalf("HTTP/2 call on a new connection: %+v", r)
	}
	if n := stand.log.count(h2LogLine); n != 2 {
		t.Fatalf("a second HTTP/2 connection brought the count to %d, want 2", n)
	}

	// Other routes over HTTP/2 say nothing: they are not the call being bounded.
	ch = stand.start(client, http.MethodGet, "/coddy/llm/models", nil)
	_ = stand.awaitStarted()
	_ = stand.result(ch)
	if n := stand.log.count(h2LogLine); n != 2 {
		t.Fatalf("a listing over HTTP/2 logged the residual: %d lines", n)
	}
}

// The same relay and an HTTP/1.1 client: probed, and no line about HTTP/2.
func TestMountLivenessAnHTTP1ClientOfATLSRelayIsProbedAndNotLogged(t *testing.T) {
	stand := newLiveStand(t, standOptions{tls: true})
	stand.requireUserTimeout()
	stand.node.releaseAll()
	ch := stand.start(stand.h1Client(nil), http.MethodPost, completionsPath, nil)
	wantOption(t, "HTTP/1.1 call to a TLS relay", stand.awaitStarted(), 30*time.Second)
	if r := stand.result(ch); r.err != nil || r.proto != 1 {
		t.Fatalf("HTTP/1.1 call to a TLS relay: %+v", r)
	}
	if n := stand.log.count(h2LogLine); n != 0 {
		t.Fatalf("an HTTP/1.1 client was logged as HTTP/2 %d times", n)
	}
	// The TCP socket under the TLS connection is the one that carried the option.
	if tcpUnder(stand.conns[0]) == nil {
		t.Fatal("the relay's TLS connection has no TCP socket under it")
	}
}

// A relay that cannot probe (an HTTP/1.1 client on a platform without the
// option, or a server built without the hook) serves the call all the same.
func TestMountLivenessWithoutTheHookServesTheCall(t *testing.T) {
	stand := newLiveStand(t, standOptions{noHook: true})
	stand.node.releaseAll()
	ch := stand.start(stand.h1Client(nil), http.MethodPost, completionsPath, nil)
	c := stand.awaitStarted()
	if c.relayOptionErr == nil && c.relayOption != 0 {
		t.Fatalf("an option was set without the hook: %v", c.relayOption)
	}
	if r := stand.result(ch); r.err != nil || r.status != http.StatusOK {
		t.Fatalf("a call without the hook: %+v", r)
	}
	if n := stand.log.count(h2LogLine); n != 0 {
		t.Fatalf("an HTTP/1.1 client was logged as HTTP/2 %d times", n)
	}
}

// The propagation of a client that is gone: whatever the way the relay learns
// of it (an abrupt close, a socket the kernel aborted that answers ETIMEDOUT
// to a write, or to a read), the relay's request context ends, the upstream
// request is cancelled and the node's handler - its stream slot - is released
// in the same round trip. This is the plumbing under the user timeout; the
// kernel mechanism itself is the opt-in test of this package.
func TestMountLivenessADeadClientEndsTheNodesCall(t *testing.T) {
	const within = time.Second
	for _, node := range []string{"direct node", "tunnel node"} {
		for _, down := range []string{"h1", "h1 over plain HTTP", "h2"} {
			for _, event := range []string{"close", "ETIMEDOUT on write", "ETIMEDOUT on read"} {
				t.Run(node+"/"+down+"/"+event, func(t *testing.T) {
					stand := newLiveStand(t, standOptions{
						tls:    down != "h1 over plain HTTP",
						tunnel: node == "tunnel node",
						fault:  event != "close",
					})
					var mu sync.Mutex
					var raw net.Conn
					capture := func(c net.Conn) { mu.Lock(); raw = c; mu.Unlock() }
					var client *http.Client
					if down == "h2" {
						client = stand.h2Client(capture)
					} else {
						client = stand.h1Client(capture)
					}
					res := stand.openStream(client)
					defer func() { _ = res.Body.Close() }()
					wantProto := 1
					if down == "h2" {
						wantProto = 2
					}
					if res.ProtoMajor != wantProto {
						t.Fatalf("the client spoke HTTP/%d, want %d", res.ProtoMajor, wantProto)
					}
					if stand.node.inUse.Load() != 1 {
						t.Fatalf("the node holds %d calls, want 1", stand.node.inUse.Load())
					}

					start := time.Now()
					switch event {
					case "close":
						mu.Lock()
						_ = raw.Close()
						mu.Unlock()
					case "ETIMEDOUT on write":
						stand.failWrites()
					case "ETIMEDOUT on read":
						stand.failReads()
					}

					end, ok := stand.awaitEnded(within)
					if !ok {
						t.Fatalf("the node's call outlived its dead client by more than %v", within)
					}
					if !end.cancelled {
						t.Fatal("the node's call ended without its context being cancelled")
					}
					if !stand.node.waitIdle(within) {
						t.Fatalf("the node still holds %d calls", stand.node.inUse.Load())
					}
					t.Logf("node freed %v after the client died", time.Since(start).Round(time.Millisecond))
				})
			}
		}
	}
}

// The real relay is the live peer of the node's health check: it answers every
// ping, so a call that stays silent for many times the ping interval is never
// cut. (The cut of a relay that does not answer is the test of internal/swarm,
// where the relay side can be muted.)
func TestMountLivenessTunnelNodeKeepsARealRelayWhileACallIsSilent(t *testing.T) {
	stand := newLiveStand(t, standOptions{
		tunnel: true,
		// One heartbeat, then nothing for the whole test.
		heartbeat:       time.Hour,
		nodePingAfter:   100 * time.Millisecond,
		nodePingTimeout: 300 * time.Millisecond,
	})
	ch := stand.start(stand.h1Client(nil), http.MethodPost, completionsPath, nil)
	_ = stand.awaitStarted()

	// Fifteen ping intervals, and ten times the time the node waits for an answer.
	if end, cut := stand.awaitEnded(1500 * time.Millisecond); cut {
		t.Fatalf("a live relay's tunnel was closed by the node's health check (cancelled=%v)", end.cancelled)
	}
	if stand.node.inUse.Load() != 1 {
		t.Fatalf("the node holds %d calls, want 1", stand.node.inUse.Load())
	}
	stand.node.releaseAll()
	if r := stand.result(ch); r.err != nil || r.status != http.StatusOK {
		t.Fatalf("the call after the silence: %+v", r)
	}
}
