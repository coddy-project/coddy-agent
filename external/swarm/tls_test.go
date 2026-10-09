//go:build swarm

package swarm

// TLS at the relay (docs/plans/remote-model-provider-tls-builtin.md): with swarm.tls.client_ca_file the handshake requires a certificate
// that chains to the CA and refuses a peer without one. That is all TLS does here: the application reads no identity out of the
// certificate, so a certificate is no credential and opens nothing. A relay client is told apart by its token alone.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type testCA struct {
	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	file string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "swarm test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	ca := &testCA{dir: t.TempDir(), cert: cert, key: key}
	ca.file = filepath.Join(ca.dir, "ca.pem")
	if err := os.WriteFile(ca.file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return ca
}

// leaf issues a certificate; names are DNS names, notAfter bounds its life.
func (ca *testCA) leaf(t *testing.T, server bool, names []string, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"},
		DNSNames:  names,
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

func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// mtlsRelay serves a relay over TLS that requires a client certificate; the relay holds one bearer entry.
func mtlsRelay(t *testing.T, ca *testCA, node *recordingNode) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.Clients = []config.SwarmClient{
		{Name: "bearer", Token: "bearer-secret", Scope: config.ScopeSharedModels, Nodes: []string{"nas02"}},
	}
	cfg.Swarm.TLS = config.SwarmTLSConfig{ClientCAFile: ca.file}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	registerRecording(t, srv, "nas02", node)
	tlsCfg, err := ClientCertTLS(cfg.Swarm.TLS)
	if err != nil {
		t.Fatal(err)
	}
	server := ca.leaf(t, true, nil, time.Now().Add(time.Hour))
	tlsCfg.Certificates = []tls.Certificate{server}
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = tlsCfg
	ts.EnableHTTP2 = true
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return srv, ts
}

func (ca *testCA) client(cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{RootCAs: ca.pool(), MinVersion: tls.VersionTLS12}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}, Timeout: 10 * time.Second}
}

func get(t *testing.T, c *http.Client, base, path, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

const mountModels = "/swarm/nodes/nas02/coddy/llm/models"

// A relay client is a bearer entry: its token is the credential, and a certificate neither stands for it nor adds to it.
func TestAnEntryIsAuthenticatedByItsTokenAlone(t *testing.T) {
	ca := newTestCA(t)
	node := newRecordingNode(t)
	_, ts := mtlsRelay(t, ca, node)
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	bob := ca.leaf(t, false, []string{"bearer-secret"}, time.Now().Add(time.Hour)) // a certificate that spells the token is still no token

	cases := []struct {
		name  string
		cert  *tls.Certificate
		token string
		want  int
	}{
		{"the entry's token over a certificate", &alice, "bearer-secret", 200},
		{"the entry's token over another certificate", &bob, "bearer-secret", 200},
		{"a certificate alone", &alice, "", 401},
		{"a certificate that spells the token, alone", &bob, "", 401},
		{"a wrong token over a certificate", &alice, "wrong", 401},
	}
	for _, tc := range cases {
		node.reset()
		code, _ := get(t, ca.client(tc.cert), ts.URL, mountModels, tc.token)
		if code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, code, tc.want)
		}
		if (code == 200) != (len(node.requests()) > 0) {
			t.Errorf("%s: status %d but node requests %v", tc.name, code, node.requests())
		}
	}

	// The node sees its own credential and never a client's.
	node.reset()
	get(t, ca.client(&alice), ts.URL, mountModels, "bearer-secret")
	for _, line := range node.requests() {
		if line != "GET /coddy/llm/models auth=Bearer node-secret q=" {
			t.Errorf("the node saw %q", line)
		}
	}
}

// A certificate never opens the full class, nor any route of the relay: the full token does, over the same connection.
func TestACertificateNeverOpensTheFullClass(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, newRecordingNode(t))
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	c := ca.client(&alice)
	for _, path := range []string{"/swarm/nodes", "/swarm/sessions", "/swarm/topology"} {
		if code, _ := get(t, c, ts.URL, path, ""); code != http.StatusUnauthorized {
			t.Errorf("GET %s with a certificate = %d, want 401", path, code)
		}
	}
	if code, _ := get(t, c, ts.URL, "/swarm/nodes", fullToken); code != http.StatusOK {
		t.Errorf("the full token over the same connection = %d", code)
	}
}

// The handshake admits a peer by its chain: no certificate, a certificate of another authority and an expired one never reach the gate.
func TestTheHandshakeRequiresACertificateThatChainsToTheCA(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, newRecordingNode(t))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/swarm/info", nil)
	other := newTestCA(t)
	stranger := other.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	expired := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(-time.Minute))
	for name, c := range map[string]*http.Client{"none": ca.client(nil), "another authority": ca.client(&stranger), "expired": ca.client(&expired)} {
		if res, err := c.Do(req); err == nil {
			_ = res.Body.Close()
			t.Errorf("%s: a peer that does not chain to the CA got %d on /swarm/info", name, res.StatusCode)
		}
	}
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	if code, _ := get(t, ca.client(&alice), ts.URL, "/swarm/info", ""); code != http.StatusOK {
		t.Errorf("a certificate of the authority = %d", code)
	}
}

// Nothing about the certificate is read per request: a client of the relay behaves the same over HTTP/2.
func TestATokenOverACertificateWorksOverHTTP2(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, newRecordingNode(t))
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
	req.Header.Set("Authorization", "Bearer bearer-secret")
	res, err := ca.client(&alice).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.ProtoMajor != 2 {
		t.Skipf("the connection is HTTP/%d", res.ProtoMajor)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("over HTTP/2: %d", res.StatusCode)
	}
}

func TestClientCertTLSBuildsTheListenerConfig(t *testing.T) {
	ca := newTestCA(t)
	if cfg, err := ClientCertTLS(config.SwarmTLSConfig{}); err != nil || cfg != nil {
		t.Errorf("no CA: %v %v, want no config", cfg, err)
	}
	cfg, err := ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: ca.file})
	if err != nil || cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("a CA means the handshake requires a certificate: %+v %v", cfg, err)
	}
	if _, err := ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: filepath.Join(ca.dir, "absent.pem")}); err == nil {
		t.Error("a missing CA file must be an error")
	}
	bad := filepath.Join(ca.dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	if _, err := ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: bad}); err == nil {
		t.Error("a CA file with no certificate must be an error")
	}
}
