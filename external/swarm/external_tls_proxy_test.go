//go:build swarm

package swarm

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// System roots are cached process-wide. The child gets an isolated trust store
// before any HTTPS request; no Coddy CA setting or skipped verification is used.
func TestExternalTLSProxy(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("SSL_CERT_FILE does not isolate native system trust stores on this platform")
	}
	if os.Getenv("CODDY_EXTERNAL_TLS_CHILD") == "1" {
		t.Run("TCPForwarder", externalTLSGate)
		t.Run("HTTPReverseProxyCannotCarryTunnel", externalTLSHTTPReverseProxyGate)
		return
	}
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local external TLS gate"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rawKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rawKey}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := filepath.Join(dir, "empty-roots")
	if err := os.Mkdir(roots, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestExternalTLSProxy$", "-test.v", "-test.timeout=45s")
	cmd.Env = append(os.Environ(), "CODDY_EXTERNAL_TLS_CHILD=1", "CODDY_EXTERNAL_TLS_CERT="+certPath, "CODDY_EXTERNAL_TLS_KEY="+keyPath, "SSL_CERT_FILE="+certPath, "SSL_CERT_DIR="+roots, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=*")
	platform.AdaptCommand(cmd)
	out, err := cmd.CombinedOutput()
	t.Logf("isolated system-trust gate:\n%s", out)
	if err != nil {
		t.Fatalf("external TLS gate: %v", err)
	}
}

// externalTLSForwarder models HAProxy mode tcp with bind ... ssl: TLS ends
// here and decrypted bytes go unchanged to a loopback HTTP backend. It is not
// an HTTP reverse proxy, and it does not parse the tunnel's 200 response.
type externalTLSForwarder struct {
	listener net.Listener
	muted    atomic.Bool
	mu       sync.Mutex
	conns    []net.Conn
	closed   bool
	wg       sync.WaitGroup
}

func newExternalTLSForwarder(t *testing.T, backend string, cert tls.Certificate) *externalTLSForwarder {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	p := &externalTLSForwarder{listener: ln}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			front, err := ln.Accept()
			if err != nil {
				return
			}
			back, err := net.DialTimeout("tcp", backend, time.Second)
			if err != nil {
				_ = front.Close()
				continue
			}
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				_ = front.Close()
				_ = back.Close()
				return
			}
			p.conns = append(p.conns, front, back)
			p.mu.Unlock()
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer func() { _ = front.Close(); _ = back.Close() }()
				done := make(chan struct{})
				go func() {
					p.copy(back, front)
					_ = front.Close()
					_ = back.Close()
					close(done)
				}()
				p.copy(front, back)
				_ = front.Close()
				_ = back.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		p.closed = true
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return p
}

func (p *externalTLSForwarder) copy(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.muted.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *externalTLSForwarder) URL() string { return "https://" + p.listener.Addr().String() }

func externalTLSGate(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(os.Getenv("CODDY_EXTERNAL_TLS_CERT"), os.Getenv("CODDY_EXTERNAL_TLS_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = "client-secret"
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(srv.Handler())
	t.Cleanup(backend.Close)
	t.Cleanup(srv.Close)
	edge := newExternalTLSForwarder(t, backend.Listener.Addr().String(), cert)
	delivered, canceled := make(chan struct{}, 8), make(chan struct{}, 8)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coddy/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			for _, line := range []string{": heartbeat\n\n", "data: chunk-1\n\n", "data: chunk-2\n\n"} {
				_, _ = io.WriteString(w, line)
				w.(http.Flusher).Flush()
				select {
				case <-delivered:
				case <-r.Context().Done():
					return
				}
			}
			<-r.Context().Done()
			canceled <- struct{}{}
		case "/coddy/hold":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: held\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			canceled <- struct{}{}
		default:
			_, _ = fmt.Fprintf(w, "%s|%s|%s", r.URL.Path, r.Header.Get("Authorization"), r.Proto)
		}
	})
	client, err := swarmdto.NewClient(swarmdto.JoinOptions{RelayURL: edge.URL(), Name: "inner", PairingToken: "pair-secret", NodeToken: "node-secret", Handler: handler, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	start := func() chan error {
		done := make(chan error, 1)
		go func() {
			done <- swarmdto.DialTunnel(ctx, swarmdto.TunnelOptions{RelayURL: edge.URL(), Node: "inner", LeaseSecret: client.LeaseSecret(), Handler: handler, PingAfter: 100 * time.Millisecond, PingTimeout: 300 * time.Millisecond})
		}()
		return done
	}
	done := start()
	externalTLSWait(t, func() bool {
		n, ok := srv.registry.Node("inner")
		return ok && n.Transport != nil && n.Transport.Alive()
	}, "tunnel online")
	hc := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	t.Cleanup(hc.CloseIdleConnections)
	request := func(c context.Context, path, token string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(c, http.MethodGet, edge.URL()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	read := func(path, token string) (int, string) {
		t.Helper()
		res := request(ctx, path, token)
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(b)
	}
	mount := swarmdto.MountPath + "inner"
	if status, body := read(mount+"/coddy/ping", "client-secret"); status != 200 || !strings.Contains(body, "Bearer node-secret|HTTP/2.0") {
		t.Fatalf("tunnel route: %d %s", status, body)
	}
	if status, _ := read(mount+"/coddy/ping", "wrong"); status != 401 {
		t.Fatalf("wrong bearer: %d", status)
	}
	t.Log("HTTPS system trust, plain HTTP backend, reverse HTTP/2 tunnel and bearer gate passed")
	// Register is also the production heartbeat operation. Repeating it must
	// renew the lease without dropping the existing tunnel.
	before, _ := srv.registry.Node("inner")
	if err := client.Register(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := srv.registry.Node("inner")
	if after.Info.Generation <= before.Info.Generation || after.Transport != before.Transport {
		t.Fatal("heartbeat replaced or failed to renew tunnel")
	}
	// Several scaled idle-ping periods prove that the byte forwarder carries
	// both ping and ACK frames after the HTTP acceptance response has ended.
	time.Sleep(800 * time.Millisecond)
	if status, body := read(mount+"/coddy/ping", "client-secret"); status != 200 {
		t.Fatalf("idle ping survival: %d %s", status, body)
	}
	t.Log("lease heartbeat and idle HTTP/2 ping/ACK survival passed")
	streamCtx, streamCancel := context.WithCancel(ctx)
	res := request(streamCtx, mount+"/coddy/stream", "client-secret")
	scan := bufio.NewScanner(res.Body)
	for _, want := range []string{": heartbeat", "data: chunk-1", "data: chunk-2"} {
		if !scan.Scan() || scan.Text() != want {
			t.Fatalf("SSE: got %q, want %q (%v)", scan.Text(), want, scan.Err())
		}
		if !scan.Scan() || scan.Text() != "" {
			t.Fatal("SSE frame missing blank line")
		}
		delivered <- struct{}{}
	}
	streamCancel()
	_ = res.Body.Close()
	externalTLSSignal(t, canceled, "stream cancellation reached node")
	t.Log("incremental SSE heartbeat/chunks and cancellation passed")
	// A vanished peer accepts bytes but answers no frames. This differs from
	// graceful EOF: HTTP/2 health checks must release the node's active work.
	held := request(ctx, mount+"/coddy/hold", "client-secret")
	heldScan := bufio.NewScanner(held.Body)
	if !heldScan.Scan() || heldScan.Text() != "data: held" {
		t.Fatal("hold did not start")
	}
	edge.muted.Store(true)
	externalTLSSignal(t, canceled, "vanished peer canceled active handler")
	select {
	case err := <-done:
		t.Logf("vanished-peer DialTunnel ended: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("node did not detect unanswered pings")
	}
	_ = held.Body.Close()
	edge.muted.Store(false)
	done = start()
	externalTLSWait(t, func() bool {
		n, ok := srv.registry.Node("inner")
		return ok && n.Transport != nil && n.Transport != before.Transport && n.Transport.Alive()
	}, "redial replaces dead tunnel")
	if status, body := read(mount+"/coddy/ping", "client-secret"); status != 200 {
		t.Fatalf("redial route: %d %s", status, body)
	}
	t.Log("blackholed peer detection and redial routing passed")
	// The direct leg has its own external TLS terminator and uses ordinary
	// system-trusted HTTPS; the backend itself has no TLS listener.
	directBackend := httptest.NewServer(handler)
	t.Cleanup(directBackend.Close)
	directEdge := newExternalTLSForwarder(t, directBackend.Listener.Addr().String(), cert)
	srv.registry.SetEgressPolicy(netx.EgressPolicy{AllowLoopback: true})
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{Name: "direct", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect, AdvertiseURL: directEdge.URL(), InstanceUUID: "direct-uuid", Token: "direct-secret"}); err != nil {
		t.Fatal(err)
	}
	if status, body := read(swarmdto.MountPath+"direct/coddy/ping", "client-secret"); status != 200 || !strings.Contains(body, "Bearer direct-secret|HTTP/1.1") {
		t.Fatalf("direct HTTPS leg: %d %s", status, body)
	}
	t.Log("direct mount through system-trusted HTTPS to loopback HTTP passed")
	outerCfg := &config.Config{}
	outerCfg.Swarm.Host = "127.0.0.1"
	outerCfg.Swarm.AuthToken = "outer-secret"
	outerCfg.Swarm.Upstreams = []config.SwarmUpstream{{Name: "relay", Kind: swarmdto.KindRelay, URL: edge.URL(), Token: "client-secret"}}
	outer, err := New(outerCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(outer.Close)
	outerBackend := httptest.NewServer(outer.Handler())
	t.Cleanup(outerBackend.Close)
	outerEdge := newExternalTLSForwarder(t, outerBackend.Listener.Addr().String(), cert)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, outerEdge.URL()+swarmdto.MountPath+"relay"+mount+"/coddy/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer outer-secret")
	upstreamResponse, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	upstreamBody, err := io.ReadAll(upstreamResponse.Body)
	_ = upstreamResponse.Body.Close()
	if err != nil || upstreamResponse.StatusCode != http.StatusOK || !strings.Contains(string(upstreamBody), "Bearer node-secret|HTTP/2.0") {
		t.Fatalf("configured HTTPS relay upstream: %d %s (%v)", upstreamResponse.StatusCode, upstreamBody, err)
	}
	t.Log("configured HTTPS relay upstream and nested tunnel mount passed")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel context cancellation hung")
	}
}

func externalTLSWait(t *testing.T, ok func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(label)
}

func externalTLSSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(label)
	}
}

// An HTTP proxy cannot forward the reverse-role HTTP/2 bytes following the
// finite 200 acceptance body. This passing negative test records the deployment
// constraint rather than leaving a deliberately failing test in the suite.
func externalTLSHTTPReverseProxyGate(t *testing.T) {
	srv, lease := tunnelRelayWithLease(t)
	backend := httptest.NewServer(srv.Handler())
	t.Cleanup(backend.Close)
	t.Cleanup(srv.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(os.Getenv("CODDY_EXTERNAL_TLS_CERT"), os.Getenv("CODDY_EXTERNAL_TLS_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	reverseProxy := httputil.NewSingleHostReverseProxy(target)
	reverseProxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.URL.Path == swarmdto.TunnelPath && res.StatusCode == http.StatusOK {
			accepted <- struct{}{}
		}
		return nil
	}
	proxy := httptest.NewUnstartedServer(reverseProxy)
	proxy.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	proxy.StartTLS()
	t.Cleanup(proxy.Close)
	hc := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	t.Cleanup(hc.CloseIdleConnections)
	request := func(path string) (int, string) {
		t.Helper()
		res, err := hc.Get(proxy.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	if status, body := request("/swarm/info"); status != http.StatusOK {
		t.Fatalf("L7 HTTPS control request: %d %s", status, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var hits atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- swarmdto.DialTunnel(ctx, swarmdto.TunnelOptions{RelayURL: proxy.URL, Node: "inner", LeaseSecret: lease.LeaseSecret, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }), PingAfter: 100 * time.Millisecond, PingTimeout: 200 * time.Millisecond})
	}()
	// The broken tunnel can disappear before a registry poll sees it. Observe
	// its acceptance at the proxy, then probe while the client context is live:
	// even a working byte forwarder would fail after cancellation.
	externalTLSSignal(t, accepted, "HTTP proxy never reached the tunnel handshake")
	if status, body := request(swarmdto.MountPath + "inner/coddy/ping"); status != http.StatusBadGateway {
		t.Fatalf("L7 proxy should refuse the mounted request: %d %s", status, body)
	}
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP proxy left the tunnel running")
	}
	if ctx.Err() != nil {
		t.Fatalf("tunnel rejection depended on context expiry: %v", ctx.Err())
	}
	if hits.Load() != 0 {
		t.Fatal("HTTP proxy unexpectedly carried the mounted request to the node")
	}
	t.Logf("ordinary L7 HTTPS reverse proxy does not preserve current tunnel; DialTunnel result: %v", err)
}
