//go:build http && swarm

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	relaysrv "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// System roots are process-wide: isolate trust before the relay first dials
// HTTPS. These are ordinary httptest certificates, not a Coddy PKI fixture.
func TestSharedExternalTLSProxy(t *testing.T) {
	if os.Getenv("CODDY_SHARED_EXTERNAL_PROXY_CHILD") != "1" {
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			t.Skip("SSL_CERT_FILE does not isolate the native system trust store")
		}
		certServer := httptest.NewTLSServer(http.NotFoundHandler())
		cert := certServer.Certificate()
		certServer.Close()
		dir := t.TempDir()
		roots := filepath.Join(dir, "roots.pem")
		if err := os.WriteFile(roots, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
			t.Fatal(err)
		}
		empty := filepath.Join(dir, "empty")
		if err := os.Mkdir(empty, 0700); err != nil {
			t.Fatal(err)
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestSharedExternalTLSProxy$", "-test.v", "-test.timeout=60s")
		cmd.Env = append(os.Environ(), "CODDY_SHARED_EXTERNAL_PROXY_CHILD=1", "SSL_CERT_FILE="+roots, "SSL_CERT_DIR="+empty,
			"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
		platform.AdaptCommand(cmd)
		out, err := cmd.CombinedOutput()
		t.Logf("system-trusted external HTTPS regression:\n%s", out)
		if err != nil {
			t.Fatalf("external proxy child: %v", err)
		}
		return
	}
	for _, mounted := range []bool{false, true} {
		name := "Direct"
		if mounted {
			name = "SwarmMount"
		}
		t.Run(name, func(t *testing.T) {
			t.Run("StagedSSEAndAuth", func(t *testing.T) { sharedExternalProxyStream(t, mounted) })
			t.Run("CancellationReleasesSlot", func(t *testing.T) { sharedExternalProxyCancel(t, mounted) })
			t.Run("MissedProbeReleasesSlot", func(t *testing.T) { sharedExternalProxyMissedProbe(t, mounted) })
		})
	}
}

// An L7 proxy is sufficient for a direct shared-model HTTP call. It is not
// used for the reverse-role HTTP/2 tunnel (covered by the Swarm proxy tests).
func sharedExternalProxyEdge(t *testing.T, backend string) string {
	t.Helper()
	u, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	edge := httptest.NewTLSServer(fullDuplexEdge(proxy))
	t.Cleanup(edge.Close)
	return edge.URL
}

type sharedExternalProxyFixture struct {
	fx     *sharedFixture
	client *http.Client
	base   string
	token  string
	node   *probeListener
	relay  *probeListener
}

func newSharedExternalProxyFixture(t *testing.T, mounted bool) *sharedExternalProxyFixture {
	t.Helper()
	fx := newSharedFixture(t, withFakeClock(), withSharedConfig(func(c *config.Config) {
		c.HTTPServer.SharedModels.MaxStreams = 1
	}))
	addr, node := serveWithProbe(t, fx)
	edge := sharedExternalProxyEdge(t, "http://"+addr)
	f := &sharedExternalProxyFixture{fx: fx, base: edge, token: sharedTestSharedTok, node: node,
		client: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}}
	t.Cleanup(f.client.CloseIdleConnections)
	if mounted {
		cfg := &config.Config{}
		cfg.Swarm.Host = "127.0.0.1"
		cfg.Swarm.AuthToken = "external-proxy-relay-main"
		cfg.Swarm.Clients = []config.SwarmClient{{Name: "reader", Token: "external-proxy-reader", Scope: config.ScopeSharedModels,
			Nodes: []string{"node"}, MaxStreams: 1}}
		cfg.Swarm.Upstreams = []config.SwarmUpstream{{Name: "node", Kind: swarmdto.KindAgent, URL: edge, Token: sharedTestSharedTok}}
		relay, err := relaysrv.New(cfg, fx.log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(relay.Close)
		backend := httptest.NewUnstartedServer(relay.Handler())
		f.relay = &probeListener{Listener: backend.Listener}
		backend.Listener = f.relay
		backend.Config.ConnContext = httpx.ConnContext
		backend.Start()
		t.Cleanup(backend.Close)
		f.base = sharedExternalProxyEdge(t, backend.URL) + swarmdto.MountPath + "node"
		f.token = "external-proxy-reader"
	}
	return f
}

func (f *sharedExternalProxyFixture) request(t *testing.T, ctx context.Context, path, token, probe string) *http.Response {
	t.Helper()
	var body io.Reader
	if path == llm.CoddyCompletionsPath {
		raw, err := json.Marshal(wireReq(sharedTestAlias))
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if path == llm.CoddyCompletionsPath && probe != "" {
		req.Header.Set(llm.CoddyProbeHeader, probe)
	} else if probe != "" {
		req.Header.Set(llm.CoddyProbeIDHeader, probe)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func sharedExternalProxyChunk(t *testing.T, rd *sseReader, want string) {
	t.Helper()
	for {
		ev, ok := rd.next(5 * time.Second)
		if !ok {
			t.Fatal("stream ended before chunk")
		}
		if ev.comment != "" {
			continue
		}
		var chunk llm.WireChunk
		if err := json.Unmarshal(ev.raw, &chunk); err != nil || chunk.Type != llm.WireTypeChunk || chunk.TextDelta != want {
			t.Fatalf("chunk: %s (%v), want %q", ev.raw, err, want)
		}
		return
	}
}

func (f *sharedExternalProxyFixture) boundSockets(t *testing.T) []*probeConn {
	t.Helper()
	var conns []*probeConn
	for _, ln := range []*probeListener{f.node, f.relay} {
		if ln == nil {
			continue
		}
		var bounded *probeConn
		waitFor(t, "the shared-call backend socket bound", func() bool {
			ln.mu.Lock()
			defer ln.mu.Unlock()
			for _, c := range ln.conns {
				if c.userTimeoutQuiet() == httpx.LivenessBound-httpx.LivenessHeartbeat {
					bounded = c
					return true
				}
			}
			return false
		})
		conns = append(conns, bounded)
	}
	return conns
}

func sharedExternalProxyStream(t *testing.T, mounted bool) {
	f := newSharedExternalProxyFixture(t, mounted)
	bad := f.request(t, context.Background(), llm.CoddyCompletionsPath, "wrong", "1")
	if bad.StatusCode != http.StatusUnauthorized || bad.Header.Get(llm.CoddyProbeHeader) != "" || f.fx.stub.callCount() != 0 {
		t.Fatalf("auth refusal: %d, probe %q, calls %d", bad.StatusCode, bad.Header.Get(llm.CoddyProbeHeader), f.fx.stub.callCount())
	}
	_ = bad.Body.Close()
	proceed := make(chan struct{})
	f.fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, chunk func(llm.StreamChunk)) (*llm.Response, error) {
		chunk(llm.StreamChunk{TextDelta: "first"})
		select {
		case <-proceed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		chunk(llm.StreamChunk{TextDelta: "second"})
		return &llm.Response{Content: "firstsecond", InputTokens: 3, OutputTokens: 2}, nil
	}
	resp := f.request(t, context.Background(), llm.CoddyCompletionsPath, f.token, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d, %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	rd := newSSEReader(t, resp)
	sharedExternalProxyChunk(t, rd, "first") // cannot arrive if either proxy buffers until completion
	var sockets []*probeConn
	if runtime.GOOS == "linux" {
		sockets = f.boundSockets(t)
	}
	// The heartbeat goroutine arms its timer on its own schedule, and the first
	// chunk can reach the reader before it has: an advance before that would
	// put the comment a whole interval past the moved clock. The heartbeat and
	// the stall guard are both armed before the clock moves.
	waitFor(t, "the heartbeat and the stall guard to arm", func() bool { return f.fx.clk.armed() >= 2 })
	f.fx.clk.Advance(httpx.LivenessHeartbeat)
	ev, ok := rd.next(5 * time.Second)
	if !ok || ev.comment != "hb" {
		t.Fatalf("incremental heartbeat: %+v", ev)
	}
	close(proceed)
	sharedExternalProxyChunk(t, rd, "second")
	terminal := terminalEvents(rd.all())
	if len(terminal) != 1 || terminal[0].typ != llm.WireTypeFinal || !strings.Contains(string(terminal[0].raw), "firstsecond") {
		t.Fatalf("terminal frames: %+v", terminal)
	}
	waitFor(t, "normal completion slot release", func() bool { return f.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)) == 0 })
	for _, c := range sockets {
		waitFor(t, "TCP_USER_TIMEOUT restored on the keep-alive backend", func() bool { return c.userTimeoutQuiet() == 0 })
	}
}

func sharedExternalProxyCancel(t *testing.T, mounted bool) {
	f := newSharedExternalProxyFixture(t, mounted)
	started := f.fx.holdCalls()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp := f.request(t, ctx, llm.CoddyCompletionsPath, f.token, "1")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	_ = resp.Body.Close()
	waitFor(t, "client cancellation through HTTPS frees node slot", func() bool { return f.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)) == 0 })
	// Admission of a replacement also proves the scoped relay slot was freed.
	next := f.request(t, context.Background(), llm.CoddyCompletionsPath, f.token, "")
	if next.StatusCode != http.StatusOK {
		t.Fatalf("replacement after cancellation: %d", next.StatusCode)
	}
	_ = next.Body.Close()
}

func sharedExternalProxyMissedProbe(t *testing.T, mounted bool) {
	f := newSharedExternalProxyFixture(t, mounted)
	started := f.fx.holdCalls()
	resp := f.request(t, context.Background(), llm.CoddyCompletionsPath, f.token, "1")
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	if resp.StatusCode != http.StatusOK || m == nil || m[2] != "10000" || m[3] != "35000" {
		t.Fatalf("probe confirmation: %d %q", resp.StatusCode, resp.Header.Get(llm.CoddyProbeHeader))
	}
	rd := newSSEReader(t, resp)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	ping := f.request(t, context.Background(), llm.CoddyAlivePath, f.token, m[1])
	if ping.StatusCode != http.StatusNoContent {
		t.Fatalf("probe ping through HTTPS: %d", ping.StatusCode)
	}
	_ = ping.Body.Close()
	f.fx.clk.Advance(sharedProbeGrace)
	if f.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)) != 1 {
		t.Fatal("probe cut before its grace plus slack")
	}
	f.fx.clk.Advance(sharedProbeSlack)
	waitFor(t, "vanished application peer releases node slot", func() bool { return f.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)) == 0 })
	terminal := terminalEvents(rd.all())
	if len(terminal) != 1 || terminal[0].typ != llm.WireTypeError || !strings.Contains(string(terminal[0].raw), llm.WireCodeProbeLapsed) {
		t.Fatalf("probe terminal frames: %+v", terminal)
	}
	stale := f.request(t, context.Background(), llm.CoddyAlivePath, f.token, m[1])
	if stale.StatusCode != http.StatusNotFound {
		t.Fatalf("ended probe: %d", stale.StatusCode)
	}
	_ = stale.Body.Close()
	next := f.request(t, context.Background(), llm.CoddyCompletionsPath, f.token, "")
	if next.StatusCode != http.StatusOK {
		t.Fatalf("replacement after missed probes: %d", next.StatusCode)
	}
	_ = next.Body.Close()
}
