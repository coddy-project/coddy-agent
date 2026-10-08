//go:build swarm

package swarm

// A verified client certificate at the relay (docs/plans/remote-model-provider-phase3.md, 4.2; D2 closed by
// docs/plans/remote-model-provider-models/p3-d2-mtls-identity.md): per entry a token only, a certificate only, or both required and
// bound to the same entry; the identity read per request from r.TLS against the live entries; a certificate never opens the full class.

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
	"net/http/httptrace"
	"net/url"
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

// mtlsRelay serves a relay over TLS with the given client authentication; the relay holds three entries: a bearer entry,
// a certificate entry and an entry that binds both.
func mtlsRelay(t *testing.T, ca *testCA, auth string, node *recordingNode) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.Clients = []config.SwarmClient{
		{Name: "bearer", Token: "bearer-secret", Scope: config.ScopeSharedModels, Nodes: []string{"nas02"}},
		{Name: "cert", Scope: config.ScopeSharedModels, Nodes: []string{"nas02"}, CertNames: []string{"alice.example"}},
		{Name: "both", Token: "both-secret", Scope: config.ScopeSharedModels, Nodes: []string{"nas02"}, CertNames: []string{"bob.example"}},
	}
	cfg.Swarm.TLS = config.SwarmTLSConfig{ClientCAFile: ca.file, ClientAuth: auth}
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

func TestEntrySemanticsAtTheListener(t *testing.T) {
	ca := newTestCA(t)
	node := newRecordingNode(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, node)
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	bob := ca.leaf(t, false, []string{"bob.example"}, time.Now().Add(time.Hour))
	carol := ca.leaf(t, false, []string{"carol.example"}, time.Now().Add(time.Hour)) // chain-valid, mapped to no entry

	cases := []struct {
		name  string
		cert  *tls.Certificate
		token string
		want  int
	}{
		{"a bearer entry, no certificate", nil, "bearer-secret", 200},
		{"a certificate entry, the certificate alone", &alice, "", 200},
		{"a certificate entry, with an unrelated wrong bearer", &alice, "wrong", 200},
		{"an entry that binds both: both", &bob, "both-secret", 200},
		{"an entry that binds both: the token alone", nil, "both-secret", 401},
		{"an entry that binds both: the certificate alone", &bob, "", 401},
		{"an entry that binds both: its token with another entry's certificate", &alice, "both-secret", 200 /* alice's own entry admits her */},
		{"a chain-valid certificate no entry names, alone", &carol, "", 401},
		{"no credential at all", nil, "", 401},
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
	get(t, ca.client(&alice), ts.URL, mountModels, "")
	for _, line := range node.requests() {
		if line != "GET /coddy/llm/models auth=Bearer node-secret q=" {
			t.Errorf("the node saw %q", line)
		}
	}
}

// A certificate never opens the full class, nor any route of the relay beyond the mount.
func TestACertificateNeverOpensTheFullClass(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, newRecordingNode(t))
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

func TestOptionalAdmitsAPeerWithoutACertificateToTheOpenRoutes(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, newRecordingNode(t))
	c := ca.client(nil)
	if code, _ := get(t, c, ts.URL, "/swarm/info", ""); code != http.StatusOK {
		t.Errorf("GET /swarm/info without a certificate = %d", code)
	}
	if code, _ := get(t, c, ts.URL, "/swarm/nodes", fullToken); code != http.StatusOK {
		t.Errorf("a full-token client without a certificate = %d", code)
	}
}

func TestRequiredRefusesAPeerWithoutACertificateAtTheHandshake(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthRequired, newRecordingNode(t))
	c := ca.client(nil)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/swarm/info", nil)
	if res, err := c.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("a peer without a certificate got %d on /swarm/info under required", res.StatusCode)
	}
	// A certificate signed by another authority is refused too.
	other := newTestCA(t)
	stranger := other.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	cc := ca.client(&stranger)
	if res, err := cc.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("a certificate of another authority got %d", res.StatusCode)
	}
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	if code, _ := get(t, ca.client(&alice), ts.URL, mountModels, ""); code != http.StatusOK {
		t.Errorf("a certificate of the authority under required = %d", code)
	}
}

// Removing a name takes effect on the next request of an open connection.
func TestARemovedNameIsRefusedOnTheNextRequestOfAnOpenConnection(t *testing.T) {
	ca := newTestCA(t)
	node := newRecordingNode(t)
	srv, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, node)
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	c := ca.client(&alice)

	reused := false
	req, _ := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused }}))
	first, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, first.Body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("before the removal: %d", first.StatusCode)
	}

	clients := srv.scopedClients()
	var kept []config.SwarmClient
	for _, cl := range clients {
		if cl.Name != "cert" {
			kept = append(kept, cl)
		}
	}
	srv.SetClients(kept)

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
	req2 = req2.WithContext(httptrace.WithClientTrace(req2.Context(), &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused }}))
	second, err := c.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if !reused {
		t.Fatal("the second request did not reuse the connection: the test does not show an open connection")
	}
	if second.StatusCode != http.StatusUnauthorized {
		t.Errorf("after the removal, on the open connection: %d, want 401", second.StatusCode)
	}
}

// A resumed connection is judged like a fresh one.
func TestAResumedConnectionIsJudgedLikeAFreshOne(t *testing.T) {
	ca := newTestCA(t)
	node := newRecordingNode(t)
	srv, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, node)
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	cache := tls.NewLRUClientSessionCache(8)
	newClient := func() *http.Client {
		return &http.Client{Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{RootCAs: ca.pool(), Certificates: []tls.Certificate{alice}, ClientSessionCache: cache, MinVersion: tls.VersionTLS13},
			DisableKeepAlives: true, // every request is a new connection, resuming the previous session
		}, Timeout: 10 * time.Second}
	}
	call := func() (int, bool) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
		res, err := newClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode, res.TLS != nil && res.TLS.DidResume
	}
	if code, _ := call(); code != http.StatusOK {
		t.Fatalf("first: %d", code)
	}
	var kept []config.SwarmClient
	for _, cl := range srv.scopedClients() {
		if cl.Name != "cert" {
			kept = append(kept, cl)
		}
	}
	srv.SetClients(kept)
	code, resumed := call()
	if !resumed {
		t.Skip("TLS session resumption did not happen on this runtime: nothing to show")
	}
	if code != http.StatusUnauthorized {
		t.Errorf("a resumed connection of a removed name = %d, want 401", code)
	}
}

// A certificate past its end has no identity, also on a connection that outlives it.
func TestACertificateThatExpiresHasNoIdentityOnAnOpenConnection(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, newRecordingNode(t))
	short := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(2*time.Second))
	c := ca.client(&short)
	if code, _ := get(t, c, ts.URL, mountModels, ""); code != http.StatusOK {
		t.Fatalf("while valid: %d", code)
	}
	time.Sleep(2500 * time.Millisecond)
	if code, _ := get(t, c, ts.URL, mountModels, ""); code != http.StatusUnauthorized {
		t.Errorf("after NotAfter on the open connection: %d, want 401", code)
	}
}

func TestIdentityIsTheSameOverHTTP2(t *testing.T) {
	ca := newTestCA(t)
	_, ts := mtlsRelay(t, ca, config.SwarmClientAuthOptional, newRecordingNode(t))
	alice := ca.leaf(t, false, []string{"alice.example"}, time.Now().Add(time.Hour))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+mountModels, nil)
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
	if err != nil || cfg.ClientAuth != tls.VerifyClientCertIfGiven || cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("a CA alone is optional: %+v %v", cfg, err)
	}
	cfg, _ = ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: ca.file, ClientAuth: config.SwarmClientAuthRequired})
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("required: %v", cfg.ClientAuth)
	}
	if _, err := ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: filepath.Join(ca.dir, "absent.pem")}); err == nil {
		t.Error("a missing CA file must be an error")
	}
	bad := filepath.Join(ca.dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	if _, err := ClientCertTLS(config.SwarmTLSConfig{ClientCAFile: bad}); err == nil {
		t.Error("a CA file with no certificate must be an error")
	}
	_ = url.URL{}
}
