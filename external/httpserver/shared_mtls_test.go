//go:build http

package httpserver

// A verified client certificate on the direct listener (docs/plans/remote-model-provider-phase3.md, 4.4): the names of
// httpserver.shared_models.cert_names open the three LLM routes as the class mtls, each name with its own budget, and nothing else.

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
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

// mtlsFixture is the shared fixture with the handler served over TLS asking for client certificates, the way listenerTLS builds it.
func mtlsFixture(t *testing.T, auth string, opts ...sharedFixtureOption) (*sharedFixture, *mtlsCA, string) {
	t.Helper()
	ca := newMTLSCA(t)
	all := append([]sharedFixtureOption{withSharedConfig(func(c *config.Config) {
		c.HTTPServer.TLS = config.HTTPTLSConfig{ClientCAFile: ca.file, ClientAuth: auth}
		c.HTTPServer.SharedModels.CertNames = []string{"alice.example", "bob.example"}
	})}, opts...)
	fx := newSharedFixture(t, all...)
	tlsCfg, err := listenerTLS(fx.cfg.HTTPServer.TLS)
	if err != nil || tlsCfg == nil {
		t.Fatalf("listenerTLS: %v %v", tlsCfg, err)
	}
	tlsCfg.Certificates = []tls.Certificate{ca.leaf(t, true)}
	ts := httptest.NewUnstartedServer(fx.srv.Handler())
	ts.TLS = tlsCfg
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

func TestMTLSNameOpensTheThreeLLMRoutesAndNothingElse(t *testing.T) {
	fx, ca, base := mtlsFixture(t, config.SwarmClientAuthOptional)
	alice := ca.leaf(t, false, "alice.example")
	c := ca.client(&alice)

	resp := mtlsDo(t, c, http.MethodGet, base+llm.CoddyModelsPath, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing with a listed certificate and no bearer: %d", resp.StatusCode)
	}
	finish(t, mtlsDo(t, c, http.MethodPost, base+llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias)))
	waitFor(t, "the counted call", func() bool { _, ok := fx.stats().row(sharedTestAlias, "mtls", "ok"); return ok })

	for _, p := range []string{"/coddy/config", "/coddy/sessions", "/v1/models", sharedStatsPattern[len("GET "):]} {
		if got := mtlsDo(t, c, http.MethodGet, base+p, "", nil).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("%s with a certificate alone: %d, want 401", p, got)
		}
	}
}

func TestMTLSUnlistedOrMissingCertificateIsRefused(t *testing.T) {
	_, ca, base := mtlsFixture(t, config.SwarmClientAuthOptional)
	carol := ca.leaf(t, false, "carol.example")
	for name, c := range map[string]*http.Client{"unlisted": ca.client(&carol), "none": ca.client(nil)} {
		if got := mtlsDo(t, c, http.MethodGet, base+llm.CoddyModelsPath, "", nil).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, got)
		}
	}
	// A certificate no CA vouches for is no certificate: the handshake itself fails.
	other := newMTLSCA(t)
	rogue := other.leaf(t, false, "alice.example")
	rc := ca.client(&rogue)
	if resp, err := rc.Get(base + llm.CoddyModelsPath); err == nil && resp.StatusCode == http.StatusOK {
		t.Error("a certificate of another authority opened the listing")
	}
}

func TestMTLSEachNameHasItsOwnBudget(t *testing.T) {
	fx, ca, base := mtlsFixture(t, config.SwarmClientAuthOptional, withFakeClock(), withRate(60, 1, 10))
	_ = fx
	alice, bob := ca.leaf(t, false, "alice.example"), ca.leaf(t, false, "bob.example")
	ac, bc := ca.client(&alice), ca.client(&bob)
	finish(t, mtlsDo(t, ac, http.MethodPost, base+llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias)))
	resp := mtlsDo(t, ac, http.MethodPost, base+llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias))
	if e := readError(t, resp); resp.StatusCode != http.StatusTooManyRequests || e.Code != llm.WireCodeRateWindow {
		t.Fatalf("alice's second call: %d %+v", resp.StatusCode, e)
	}
	finish(t, mtlsDo(t, bc, http.MethodPost, base+llm.CoddyCompletionsPath, "", wireReq(sharedTestAlias)))
}

func TestMTLSAMainTokenStillOpensEverythingWithOrWithoutACertificate(t *testing.T) {
	_, ca, base := mtlsFixture(t, config.SwarmClientAuthOptional)
	alice := ca.leaf(t, false, "alice.example")
	for name, c := range map[string]*http.Client{"with": ca.client(&alice), "without": ca.client(nil)} {
		if got := mtlsDo(t, c, http.MethodGet, base+"/v1/models", sharedTestMainToken, nil).StatusCode; got != http.StatusOK {
			t.Errorf("%s a certificate: %d", name, got)
		}
	}
}

func TestMTLSCertificateNamesAloneCloseTheOtherRoutes(t *testing.T) {
	// No token of any class is configured, only cert_names: the gate is still on.
	_, ca, base := mtlsFixture(t, config.SwarmClientAuthOptional, withSharedConfig(func(c *config.Config) {
		c.HTTPServer.AuthToken = ""
		c.HTTPServer.SharedModels.Tokens = nil
	}))
	if got := mtlsDo(t, ca.client(nil), http.MethodGet, base+"/coddy/sessions", "", nil).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("a route other than the LLM three, open to every caller: %d", got)
	}
}

func TestMTLSRequiredRefusesAPeerWithoutACertificateAtTheHandshake(t *testing.T) {
	_, ca, base := mtlsFixture(t, config.SwarmClientAuthRequired)
	if resp, err := ca.client(nil).Get(base + llm.CoddyModelsPath); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("a peer without a certificate was served: %d", resp.StatusCode)
	}
}

func TestListenerTLSIsNilWithoutAClientCA(t *testing.T) {
	cfg, err := listenerTLS(config.HTTPTLSConfig{CertFile: "c", KeyFile: "k"})
	if err != nil || cfg != nil {
		t.Fatalf("%v %v", cfg, err)
	}
}
