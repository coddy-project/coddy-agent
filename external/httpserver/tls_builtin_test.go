//go:build http

package httpserver

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

// freeAddr is a loopback address nothing listens on at the moment.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// httpserver.tls.auto serves HTTPS with the built-in server pair; require_client_cert makes the handshake require a certificate of the
// built-in bundle. A client with the built-in client pair and the bundle gets through, a client without a certificate does not, and a
// certificate of a CA the bundle does not hold does not. Whatever got through is still only as good as its token.
func TestServeWithTheBuiltinCertificatesAdmitsByTheHandshakeAndTheTokenDecides(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.TLS = config.HTTPTLSConfig{Auto: true, RequireClientCert: true}
	}))
	if _, err := config.EnsureBuiltinTLS(fx.cfg, nil); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	opts := serveOptions(fx, slog.New(slog.NewTextHandler(logs, nil)))
	opts.ListenAddr = freeAddr(t)
	go func() { done <- Serve(ctx, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("Serve did not return")
		}
	})
	waitFor(t, "the server to listen", func() bool { return strings.Contains(logs.String(), "listening") })
	if !strings.Contains(logs.String(), "builtin=true") || !strings.Contains(logs.String(), "tls=true") || !strings.Contains(logs.String(), "client_ca=true") {
		t.Fatalf("the start did not say it serves the built-in certificates:\n%s", logs.String())
	}
	base := "https://" + opts.ListenAddr
	p := pki.PathsIn(pki.Dir(fx.cfg.Paths.Home))

	client := func(o netx.Options) *http.Client {
		cfg, err := o.TLSConfig("127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
	}
	get := func(c *http.Client, token string) (int, error) {
		req, _ := http.NewRequest(http.MethodGet, base+llm.CoddyModelsPath, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	good := client(netx.Options{CAFile: p.Bundle, CertFile: p.ClientCert, KeyFile: p.ClientKey})
	if code, err := get(good, sharedTestSharedTok); err != nil || code != http.StatusOK {
		t.Fatalf("the built-in client pair with the token: %d %v", code, err)
	}
	if code, err := get(good, ""); err != nil || code != http.StatusUnauthorized {
		t.Fatalf("a certificate is no credential: %d %v", code, err)
	}
	if _, err := get(client(netx.Options{CAFile: p.Bundle}), sharedTestSharedTok); err == nil {
		t.Fatal("a client without a certificate got through the handshake")
	}
	// A server certificate that the client does not trust is refused by the client.
	plain := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
	if _, err := get(plain, sharedTestSharedTok); err == nil {
		t.Fatal("the built-in server certificate verified against the system roots")
	}
	// A certificate of another machine's CA that this machine does not trust.
	stranger := t.TempDir()
	if _, err := pki.Ensure(stranger, pki.Want{Host: "stranger"}, nil); err != nil {
		t.Fatal(err)
	}
	sp := pki.PathsIn(stranger)
	if _, err := get(client(netx.Options{CAFile: p.Bundle, CertFile: sp.ClientCert, KeyFile: sp.ClientKey}), sharedTestSharedTok); err == nil {
		t.Fatal("a client certificate of a CA the bundle does not hold got through")
	}
	// Trusting the stranger's CA admits it on the next start, and a trusted CA is in the bundle.
	ca, _ := pki.ExportCA(stranger)
	if added, _, err := pki.Trust(pki.Dir(fx.cfg.Paths.Home), ca, time.Now()); err != nil || !added {
		t.Fatalf("trust: %v %v", added, err)
	}
	if !strings.Contains(string(readFileOrEmpty(p.Bundle)), string(ca)) {
		t.Fatal("the bundle does not hold the trusted CA")
	}
}

func readFileOrEmpty(path string) []byte {
	raw, _ := os.ReadFile(path)
	return raw
}
