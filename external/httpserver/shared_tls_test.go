//go:build http

package httpserver

// An external reverse proxy terminates mTLS; application rights belong to tokens.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type mtlsCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	file string
}

func newMTLSCA(t *testing.T) *mtlsCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	ca := &mtlsCA{cert: cert, key: key, file: filepath.Join(t.TempDir(), "ca.pem")}
	if err := os.WriteFile(ca.file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return ca
}

func (ca *mtlsCA) leaf(t *testing.T, server bool, names ...string) tls.Certificate {
	t.Helper()
	return ca.leafUntil(t, server, time.Now().Add(time.Hour), names...)
}

func (ca *mtlsCA) leafUntil(t *testing.T, server bool, notAfter time.Time, names ...string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (ca *mtlsCA) client(cert *tls.Certificate) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
}

// fullDuplexEdge is the handler of a test edge in front of a node: next, run
// full duplex. A node answers a call with its headers and a first heartbeat as
// soon as it has read the body, and an HTTP/1 server closes an inbound body once
// the handler writes headers, so the transport of a plain proxy, still reading
// for the end of the body, would fail, drop its connection to the node and end
// a call that had started. The relay's mount answers that read itself
// (declaredBody, external/swarm/mount.go); an edge in front of a node keeps the
// body open instead, as a buffering edge would.
func fullDuplexEdge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		next.ServeHTTP(w, r)
	})
}

// mtlsFixture puts the ordinary HTTP handler behind an independent mTLS edge.
func mtlsFixture(t *testing.T, opts ...sharedFixtureOption) (*sharedFixture, *mtlsCA, string) {
	t.Helper()
	ca := newMTLSCA(t)
	fx := newSharedFixture(t, opts...)
	backend := httptest.NewServer(fx.srv.Handler())
	t.Cleanup(backend.Close)
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	ts := httptest.NewUnstartedServer(fullDuplexEdge(httputil.NewSingleHostReverseProxy(target)))
	ts.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{ca.leaf(t, true)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return fx, ca, ts.URL
}

func mtlsDo(t *testing.T, c *http.Client, method, url, token string, body any) *http.Response {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A certificate alone opens nothing, whatever name it carries: the handshake admitted the peer and the gate wants a token.
func TestACertificateIsNotACredential(t *testing.T) {
	_, ca, base := mtlsFixture(t)
	for _, name := range []string{"alice.example", "shared-token", "main-token"} {
		cert := ca.leaf(t, false, name)
		c := ca.client(&cert)
		for _, p := range []string{llm.CoddyModelsPath, llm.CoddyModelsPath + "/" + sharedTestAlias + "/usage", "/coddy/config", "/coddy/sessions", "/v1/models"} {
			if got := mtlsDo(t, c, http.MethodGet, base+p, "", nil).StatusCode; got != http.StatusUnauthorized {
				t.Errorf("%s with a certificate named %q and no token: %d, want 401", p, name, got)
			}
		}
		resp := mtlsDo(t, c, http.MethodPost, base+llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("a call with a certificate named %q and no token: %d, want 401", name, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// A token over a certificate does what the token does, and nothing more: the shared token opens the three routes and the probe's ping,
// the main token everything.
func TestATokenOverACertificateDoesWhatTheTokenDoes(t *testing.T) {
	fx, ca, base := mtlsFixture(t)
	cert := ca.leaf(t, false, "alice.example")
	c := ca.client(&cert)
	if got := mtlsDo(t, c, http.MethodGet, base+llm.CoddyModelsPath, sharedTestSharedTok, nil).StatusCode; got != http.StatusOK {
		t.Fatalf("listing with the shared token: %d", got)
	}
	finish(t, mtlsDo(t, c, http.MethodPost, base+llm.CoddyCompletionsPath, sharedTestSharedTok, wireReq(sharedTestAlias)))
	// The counted class is the token's, whatever certificate rode with it.
	waitFor(t, "the counted call", func() bool { _, ok := fx.stats().row(sharedTestAlias, "shared", "ok"); return ok })
	if got := mtlsDo(t, c, http.MethodGet, base+"/coddy/sessions", sharedTestSharedTok, nil).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("the shared token on another route: %d, want 401", got)
	}
	if got := mtlsDo(t, c, http.MethodGet, base+"/v1/models", sharedTestMainToken, nil).StatusCode; got != http.StatusOK {
		t.Errorf("the main token with a certificate: %d", got)
	}
}

// A certificate gives no budget of its own: two certificates carrying one token share the token's window.
func TestTwoCertificatesWithOneTokenShareOneBudget(t *testing.T) {
	_, ca, base := mtlsFixture(t, withFakeClock(), withRate(60, 1, 10))
	alice, bob := ca.leaf(t, false, "alice.example"), ca.leaf(t, false, "bob.example")
	ac, bc := ca.client(&alice), ca.client(&bob)
	finish(t, mtlsDo(t, ac, http.MethodPost, base+llm.CoddyCompletionsPath, sharedTestSharedTok, wireReq(sharedTestAlias)))
	resp := mtlsDo(t, bc, http.MethodPost, base+llm.CoddyCompletionsPath, sharedTestSharedTok, wireReq(sharedTestAlias))
	if e := readError(t, resp); resp.StatusCode != http.StatusTooManyRequests || e.Code != llm.WireCodeRateWindow {
		t.Fatalf("a second certificate with the same token: %d %+v, want the token's window refusing it", resp.StatusCode, e)
	}
}

// The handshake admits a peer by its chain: no certificate, a certificate of another authority, and an expired one never reach the gate.
func TestTheHandshakeAdmitsByTheChainOnly(t *testing.T) {
	_, ca, base := mtlsFixture(t)
	other := newMTLSCA(t)
	rogue := other.leaf(t, false, "alice.example")
	old := ca.leafUntil(t, false, time.Now().Add(-time.Minute), "alice.example")
	for name, c := range map[string]*http.Client{"none": ca.client(nil), "another authority": ca.client(&rogue), "expired": ca.client(&old)} {
		resp, err := c.Get(base + llm.CoddyModelsPath)
		if err == nil {
			_ = resp.Body.Close()
			t.Errorf("%s: a peer that does not chain to the client CA was served: %d", name, resp.StatusCode)
		}
	}
}

// A call is pinged with the token it was made with: the id is bound to the credential of the call and a certificate does not count.
func TestTheProbePingIsBoundToTheTokenNotToTheCertificate(t *testing.T) {
	fx, ca, base := mtlsFixture(t)
	started := fx.holdCalls()
	alice, bob := ca.leaf(t, false, "alice.example"), ca.leaf(t, false, "bob.example")
	ac, bc := ca.client(&alice), ca.client(&bob)

	raw, _ := json.Marshal(wireReq(sharedTestAlias))
	req, _ := http.NewRequest(http.MethodPost, base+llm.CoddyCompletionsPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sharedTestSharedTok)
	req.Header.Set(llm.CoddyProbeHeader, "1")
	resp, err := ac.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	m := probeHeaderRE.FindStringSubmatch(resp.Header.Get(llm.CoddyProbeHeader))
	if m == nil {
		t.Fatalf("no confirmation for the call: %d", resp.StatusCode)
	}
	_ = newSSEReader(t, resp)
	<-started

	ping := func(c *http.Client, token string) int {
		r, _ := http.NewRequest(http.MethodPost, base+llm.CoddyAlivePath, nil)
		r.Header.Set(llm.CoddyProbeIDHeader, m[1])
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if got := ping(bc, sharedTestSharedTok); got != http.StatusNoContent {
		t.Errorf("the token of the call pings it, from any certificate: %d", got)
	}
	if got := ping(ac, ""); got != http.StatusUnauthorized {
		t.Errorf("the certificate that made the call, with no token: %d, want 401", got)
	}
}
