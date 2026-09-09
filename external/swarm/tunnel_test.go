//go:build swarm

package swarm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// tunnelStand is a relay plus a node that reaches it only by dialling out, the
// way a node in a closed contour has to.
type tunnelStand struct {
	relay  *httptest.Server
	srv    *Server
	secret string
	stop   context.CancelFunc
}

func newTunnelStand(t *testing.T, nodeHandler http.Handler) *tunnelStand {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = "client-secret"
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	relay := httptest.NewServer(srv.Handler())
	t.Cleanup(relay.Close)

	// The node registers with no advertised URL, which is how it says the relay
	// cannot reach it and it will open the connection itself.
	res, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name:         "inner",
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportTunnel,
		InstanceUUID: "uuid-inner",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := swarmdto.DialTunnel(ctx, swarmdto.TunnelOptions{
			RelayURL:    relay.URL,
			Node:        "inner",
			LeaseSecret: res.LeaseSecret,
			Handler:     nodeHandler,
		}); err != nil && ctx.Err() == nil {
			t.Logf("tunnel ended: %v", err)
		}
	}()

	stand := &tunnelStand{relay: relay, srv: srv, secret: res.LeaseSecret, stop: cancel}
	stand.waitOnline(t)
	return stand
}

func (s *tunnelStand) waitOnline(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if node, ok := s.srv.registry.Node("inner"); ok && node.Info.Online && node.Transport != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the tunnel never came up")
}

func (s *tunnelStand) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.relay.URL+swarmdto.MountPath+"inner"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// The point of the whole transport: a node that never opened a port is driven
// exactly like a reachable one.
func TestTunnelCarriesRequestsToAnUnreachableNode(t *testing.T) {
	var hits atomic.Int32
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"path": r.URL.Path,
			"auth": r.Header.Get("Authorization"),
		})
	}))

	status, body := stand.get(t, "/coddy/sessions")
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	if !strings.Contains(body, `"path":"/coddy/sessions"`) {
		t.Fatalf("the node did not see the request path: %s", body)
	}
	if hits.Load() != 1 {
		t.Fatalf("node handled %d requests, want 1", hits.Load())
	}
}

// A turn arrives as a stream, and it has to stay a stream all the way through a
// connection that runs backwards.
//
// The node writes a chunk and then waits to hear that it arrived before writing
// the next, so what the test observes is the delivery itself rather than the
// wall-clock gaps between arrivals. A tunnel that buffered would leave the node
// waiting: its answer reaches the client only once the handler has returned,
// and the handler cannot return until the client has seen the chunk before.
func TestTunnelStreamsWithoutBuffering(t *testing.T) {
	delivered := make(chan struct{}, 8)
	var stalled atomic.Bool
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher on the node side of the tunnel")
			return
		}
		for i := 0; i < 3; i++ {
			_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			fl.Flush()
			select {
			case <-delivered:
			case <-r.Context().Done():
				return
			case <-time.After(streamAck):
				stalled.Store(true)
				return
			}
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*streamAck)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, stand.relay.URL+swarmdto.MountPath+"inner/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer client-secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	sc := bufio.NewScanner(res.Body)
	var chunks []string
	for sc.Scan() {
		if !strings.HasPrefix(sc.Text(), "data: ") {
			continue
		}
		chunks = append(chunks, strings.TrimSpace(sc.Text()))
		select {
		case delivered <- struct{}{}:
		default:
		}
	}
	if stalled.Load() {
		t.Fatalf("the node waited %s for a chunk to reach the client, so something buffered the stream: %v", streamAck, chunks)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("the stream ended early: %v (chunks %v)", err, chunks)
	}
	want := []string{"data: chunk-0", "data: chunk-1", "data: chunk-2"}
	if !slices.Equal(chunks, want) {
		t.Fatalf("received %v, want %v", chunks, want)
	}
}

// One connection carries every request for the node, so they have to multiplex
// rather than queue behind each other.
func TestTunnelMultiplexesConcurrentRequests(t *testing.T) {
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]interface{}{"path": r.URL.Path})
	}))

	start := time.Now()
	done := make(chan int, 6)
	for i := 0; i < 6; i++ {
		go func(i int) {
			status, _ := stand.get(t, fmt.Sprintf("/coddy/sessions?n=%d", i))
			done <- status
		}(i)
	}
	for i := 0; i < 6; i++ {
		if status := <-done; status != http.StatusOK {
			t.Fatalf("request %d returned %d", i, status)
		}
	}
	// Serialised, six 100ms handlers would take at least 600ms.
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("six concurrent requests took %v, so they were not multiplexed", elapsed)
	}
}

// A tunnel needs a real connection to take over. Behind an intermediary that
// re-frames requests there is none, and saying so beats a generic failure.
func TestTunnelRefusesWhenTheConnectionCannotBeTakenOver(t *testing.T) {
	srv, res := tunnelRelayWithLease(t)
	rec := httptest.NewRecorder() // a ResponseWriter that cannot be hijacked
	req := httptest.NewRequest(http.MethodPost, swarmdto.TunnelPath, nil)
	req.Header.Set("X-Coddy-Swarm-Node", "inner")
	req.Header.Set("Authorization", "Bearer "+res.LeaseSecret)
	srv.handleTunnel(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "HTTP/1.1") {
		t.Fatalf("the refusal should explain what the deployment needs: %s", rec.Body.String())
	}
}

// tunnelRelayWithLease returns a relay holding one tunnel lease for "inner".
func tunnelRelayWithLease(t *testing.T) (*Server, swarmdto.RegisterResponse) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.PairingTokens = []string{"pair"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name:         "inner",
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportTunnel,
		InstanceUUID: "uuid-inner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, res
}

// The connection is taken over only after ownership is proved. Answering first
// and checking afterwards would hand an accept to a caller who proved nothing.
func TestTunnelProvesTheLeaseBeforeTakingTheConnection(t *testing.T) {
	srv, _ := tunnelRelayWithLease(t)

	// A recorder cannot be hijacked, so reaching the hijack check at all means
	// the credential passed. A wrong secret must stop earlier than that.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, swarmdto.TunnelPath, nil)
	req.Header.Set("X-Coddy-Swarm-Node", "inner")
	req.Header.Set("Authorization", "Bearer not-the-secret")
	srv.handleTunnel(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong secret got %d, want 401 before anything is taken over", rec.Code)
	}

	// An unknown node is refused the same way rather than being accepted and
	// then dropped.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, swarmdto.TunnelPath, nil)
	req.Header.Set("X-Coddy-Swarm-Node", "stranger")
	req.Header.Set("Authorization", "Bearer anything")
	srv.handleTunnel(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unknown node got %d, want 401", rec.Code)
	}
}

func TestTunnelNeedsTheLeaseSecret(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.PairingTokens = []string{"pair"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, swarmdto.TunnelPath, nil)
	req.Header.Set("X-Coddy-Swarm-Node", "inner")
	srv.handleTunnel(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 without a lease secret", rec.Code)
	}
}

// When the node goes away the relay must stop advertising it, or clients are
// sent into requests that will never be answered.
func TestTunnelNodeGoesOfflineWhenItDisconnects(t *testing.T) {
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if status, _ := stand.get(t, "/coddy/sessions"); status != http.StatusOK {
		t.Fatal("the tunnel should work before the node leaves")
	}

	stand.stop() // the node's context ends, closing its side

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(stand.srv.registry.Online()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the relay kept advertising a node whose connection had gone")
}

// A node that redials replaces its connection. The streams that were running on
// the old one end; a client sees a failure rather than a hang, and the next
// request goes to the new connection.
func TestTunnelReconnectReplacesTheRouteAndEndsOldWork(t *testing.T) {
	slow := make(chan struct{})
	stand := newTunnelStand(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/coddy/slow" {
			<-slow // held open until the test lets go
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"path": r.URL.Path})
	}))
	defer close(slow)

	before, _ := stand.srv.registry.Node("inner")

	inFlight := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, stand.relay.URL+swarmdto.MountPath+"inner/coddy/slow", nil)
		req.Header.Set("Authorization", "Bearer client-secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			inFlight <- err
			return
		}
		_, err = io.ReadAll(res.Body)
		_ = res.Body.Close()
		inFlight <- err
	}()
	// Let the request reach the node before the connection is replaced.
	time.Sleep(200 * time.Millisecond)

	// A second dial-out from the same node, as a reconnect would be.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = swarmdto.DialTunnel(ctx, swarmdto.TunnelOptions{
			RelayURL:    stand.relay.URL,
			Node:        "inner",
			LeaseSecret: stand.secret,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]interface{}{"served_by": "reconnected"})
			}),
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	var after Node
	for time.Now().Before(deadline) {
		n, ok := stand.srv.registry.Node("inner")
		if ok && n.Info.Generation > before.Info.Generation && n.Transport != nil {
			after = n
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after.Transport == nil {
		t.Fatal("the reconnect never replaced the transport")
	}
	if after.Transport == before.Transport {
		t.Fatal("the registry kept routing to the replaced connection")
	}

	// The work that was in flight ends rather than hanging forever.
	select {
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("a request on the replaced connection hung instead of failing")
	}

	// And the node is reachable again, over the new connection.
	status, body := stand.get(t, "/coddy/sessions")
	if status != http.StatusOK || !strings.Contains(body, "reconnected") {
		t.Fatalf("after a reconnect the node should answer on the new connection: %d %s", status, body)
	}
}
