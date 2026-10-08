package llm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// The TLS identity of a coddy row: the authority it trusts for the remote and
// the client certificate it presents, on the completion, the listing and the
// usage read alike.

type identityPKI struct {
	dir    string
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caFile string
}

func newIdentityPKI(t *testing.T) *identityPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "identity test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	p := &identityPKI{dir: t.TempDir(), caCert: cert, caKey: key}
	p.caFile = filepath.Join(p.dir, "ca.pem")
	if err := os.WriteFile(p.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *identityPKI) issue(t *testing.T, name string, serial int64, server bool) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certPath, keyPath = filepath.Join(p.dir, name+".crt"), filepath.Join(p.dir, name+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

type identityLog struct {
	mu    sync.Mutex
	peers []string // "<path> <client cn> <alpn>"
}

func (l *identityLog) add(s string) {
	l.mu.Lock()
	l.peers = append(l.peers, s)
	l.mu.Unlock()
}

func (l *identityLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.peers)
}

// identityRemote is a TLS remote signed by a private CA that requires and
// verifies a client certificate, and answers the three routes of a coddy row.
func identityRemote(t *testing.T, p *identityPKI) (*httptest.Server, *identityLog) {
	t.Helper()
	log := &identityLog{}
	certPath, keyPath := p.issue(t, "remote", 100, true)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(p.caCert)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.URL.Path + " " + r.TLS.PeerCertificates[0].Subject.CommonName + " " + r.TLS.NegotiatedProtocol)
		switch r.URL.Path {
		case CoddyCompletionsPath:
			fw := startStream(w)
			fw.text("hi")
			fw.final(&Response{Content: "hi", StopReason: "end_turn", InputTokens: 1, OutputTokens: 1})
		case CoddyModelsPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"protocol":1,"data":[{"id":"coder","revision":"r1","max_context_tokens":1000}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"supported":true,"account_wide":true,"windows":[{"id":"session","label":"3h","used_percent":10}]}`)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, log
}

func identityInput(srv *httptest.Server, proxy string, id netx.ClientTLS) ProviderInput {
	return ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: srv.URL, APIKey: "tok",
		ProxyURL: proxy, ClientTLS: id, RetryDisabled: true}
}

func exerciseRow(t *testing.T, in ProviderInput) {
	t.Helper()
	p, err := NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(context.Background(), userMsg("Hi"), nil, nil); err != nil {
		t.Fatalf("completion: %v", err)
	}
	if _, err := ListModels(context.Background(), in); err != nil {
		t.Fatalf("listing: %v", err)
	}
	if u, err := CoddyUsageForProvider(context.Background(), in, "coder"); err != nil || u == nil || !u.Supported {
		t.Fatalf("usage: %+v %v", u, err)
	}
}

func TestCoddyRowPresentsItsIdentityOnEveryRequest(t *testing.T) {
	pki := newIdentityPKI(t)
	srv, log := identityRemote(t, pki)
	cert, key := pki.issue(t, "alice", 2, false)
	exerciseRow(t, identityInput(srv, "none", netx.ClientTLS{CAFile: pki.caFile, CertFile: cert, KeyFile: key}))

	seen := log.snapshot()
	if len(seen) != 3 {
		t.Fatalf("the remote saw %d requests: %v", len(seen), seen)
	}
	for _, s := range seen {
		if s[len(s)-len(" alice http/1.1"):] != " alice http/1.1" {
			t.Errorf("request %q: want the client certificate of alice over http/1.1", s)
		}
	}
}

func TestCoddyRowWithoutIdentityIsRefusedByAnMTLSRemote(t *testing.T) {
	pki := newIdentityPKI(t)
	srv, _ := identityRemote(t, pki)
	in := identityInput(srv, "none", netx.ClientTLS{CAFile: pki.caFile})
	if _, err := ListModels(context.Background(), in); err == nil {
		t.Fatal("a remote that requires a client certificate answered a row with none")
	}
	// And a row that trusts nobody it should not fails on the remote's own certificate.
	if _, err := ListModels(context.Background(), identityInput(srv, "none", netx.ClientTLS{})); err == nil {
		t.Fatal("an unknown authority verified")
	}
}

// The identity rides the row's proxy: the CONNECT shows the route, and the
// certificate is still presented to the remote at the end of the tunnel.
func TestCoddyRowPresentsItsIdentityBehindItsProxy(t *testing.T) {
	pki := newIdentityPKI(t)
	srv, log := identityRemote(t, pki)
	var connects atomic.Int32
	var target atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "not a CONNECT", http.StatusBadRequest)
			return
		}
		connects.Add(1)
		target.Store(r.Host)
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj := w.(http.Hijacker)
		down, _, _ := hj.Hijack()
		_, _ = down.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
	}))
	t.Cleanup(proxy.Close)

	cert, key := pki.issue(t, "alice", 2, false)
	exerciseRow(t, identityInput(srv, proxy.URL, netx.ClientTLS{CAFile: pki.caFile, CertFile: cert, KeyFile: key}))
	if connects.Load() == 0 {
		t.Fatal("the row did not go through its proxy")
	}
	if got, _ := target.Load().(string); got != srv.Listener.Addr().String() {
		t.Errorf("CONNECT target %q, want %q", got, srv.Listener.Addr().String())
	}
	if len(log.snapshot()) != 3 {
		t.Fatalf("the remote saw %v", log.snapshot())
	}
}

func TestIdentityTransportKeyIsStableAndFollowsTheFiles(t *testing.T) {
	pki := newIdentityPKI(t)
	cert, key := pki.issue(t, "alice", 2, false)
	id := netx.ClientTLS{CAFile: pki.caFile, CertFile: cert, KeyFile: key}

	a, err := providerTransportWithIdentity("coddy", "none", id)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := providerTransportWithIdentity("coddy", "none", id)
	if a != b {
		t.Fatal("the same identity built two transports")
	}
	if a == mustTransport(t, "coddy", "none") {
		t.Fatal("an identity shares the transport of a row with none")
	}
	if other, _ := providerTransportWithIdentity("coddy", "http://127.0.0.1:3128", id); other == a {
		t.Fatal("two proxy settings share one transport with the same identity")
	}

	// A certificate rotated on disk builds a new transport.
	later := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(cert, later, later); err != nil {
		t.Fatal(err)
	}
	if rotated, _ := providerTransportWithIdentity("coddy", "none", id); rotated == a {
		t.Fatal("a rotated certificate kept the old transport")
	}
}

// An empty identity is exactly what it was, and no other type has one.
func TestEmptyIdentityKeepsEveryTransportItHad(t *testing.T) {
	for _, typ := range []string{"coddy", "openai", "anthropic", "neuraldeep", "codex", "devin", ""} {
		for _, setting := range []string{"", "none", "http://127.0.0.1:3128"} {
			got, err := providerTransportWithIdentity(typ, setting, netx.ClientTLS{})
			if err != nil || got != mustTransport(t, typ, setting) {
				t.Errorf("type %q setting %q: %v %v", typ, setting, got, err)
			}
		}
	}
	id := netx.ClientTLS{CertFile: "/c", KeyFile: "/k"}
	for _, typ := range []string{"openai", "anthropic", "neuraldeep", "codex", "devin", ""} {
		got, err := providerTransportWithIdentity(typ, "none", id)
		if err != nil || got != mustTransport(t, typ, "none") {
			t.Errorf("type %q must ignore an identity: %v %v", typ, got, err)
		}
	}
}

func TestIdentityWithAnUnreadableAuthorityIsABuildError(t *testing.T) {
	_, err := providerTransportWithIdentity("coddy", "none", netx.ClientTLS{CAFile: filepath.Join(t.TempDir(), "absent.pem")})
	if err == nil {
		t.Fatal("a ca_file that cannot be read must fail the build of the transport")
	}
	// Nothing is cached for a failure.
	if _, err := providerTransportWithIdentity("coddy", "none", netx.ClientTLS{CAFile: filepath.Join(t.TempDir(), "absent.pem")}); err == nil {
		t.Fatal("second attempt succeeded")
	}
}
