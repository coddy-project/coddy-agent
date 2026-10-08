package netx

import (
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
	"strings"
	"testing"
	"time"
)

type testPKI struct {
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caPEM  []byte
	dir    string
}

func newPKI(t *testing.T) *testPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testPKI{caCert: cert, caKey: key, dir: t.TempDir(),
		caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue writes a leaf certificate and key signed by the CA and returns the paths.
func (p *testPKI) issue(t *testing.T, name string, serial int64, client bool) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if client {
		usage = x509.ExtKeyUsageClientAuth
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		DNSNames:  []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	if !client {
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certPath = filepath.Join(p.dir, name+".crt")
	keyPath = filepath.Join(p.dir, name+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func (p *testPKI) caFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(p.dir, "ca.pem")
	if err := os.WriteFile(path, p.caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mtlsServer requires and verifies a client certificate and answers with the
// common name it saw.
func (p *testPKI) mtlsServer(t *testing.T) *httptest.Server {
	t.Helper()
	certPath, keyPath := p.issue(t, "server", 100, false)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(p.caCert)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, c *http.Client, url string) (string, error) {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return string(b), nil
}

func TestClientCertificateIsPresented(t *testing.T) {
	pki := newPKI(t)
	ts := pki.mtlsServer(t)
	cert, key := pki.issue(t, "alice", 2, true)
	client, err := (Options{CAFile: pki.caFile(t), CertFile: cert, KeyFile: key}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	got, err := get(t, client, ts.URL)
	if err != nil || got != "alice" {
		t.Fatalf("body %q err %v, want the server to see alice", got, err)
	}
}

func TestWithoutAClientCertificateTheHandshakeFails(t *testing.T) {
	pki := newPKI(t)
	ts := pki.mtlsServer(t)
	client, _ := (Options{CAFile: pki.caFile(t)}).HTTPClient()
	if _, err := get(t, client, ts.URL); err == nil {
		t.Fatal("a server that requires a client certificate accepted none")
	}
}

// A rotated pair is used by the next connection with no restart; a pair that
// cannot be read fails that handshake only and recovers when the file does.
func TestClientCertificateRotatesAndFailsOnlyTheHandshake(t *testing.T) {
	pki := newPKI(t)
	ts := pki.mtlsServer(t)
	cert, key := pki.issue(t, "alice", 2, true)
	opts := Options{CAFile: pki.caFile(t), CertFile: cert, KeyFile: key}
	tr, err := opts.Transport()
	if err != nil {
		t.Fatal(err)
	}
	tr.DisableKeepAlives = true
	client := &http.Client{Transport: tr}
	if got, err := get(t, client, ts.URL); err != nil || got != "alice" {
		t.Fatalf("first: %q %v", got, err)
	}

	// Rotation: new files under the same paths, a later mtime.
	bobCert, bobKey := pki.issue(t, "bob", 3, true)
	for from, to := range map[string]string{bobCert: cert, bobKey: key} {
		b, _ := os.ReadFile(from)
		if err := os.WriteFile(to, b, 0o600); err != nil {
			t.Fatal(err)
		}
		later := time.Now().Add(2 * time.Second)
		_ = os.Chtimes(to, later, later)
	}
	if got, err := get(t, client, ts.URL); err != nil || got != "bob" {
		t.Fatalf("after rotation: %q %v, want bob", got, err)
	}

	// A missing file fails the handshake, not the process, and recovers.
	saved, _ := os.ReadFile(key)
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err := get(t, client, ts.URL); err == nil {
		t.Fatal("a missing key file must fail the handshake")
	}
	if err := os.WriteFile(key, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(4 * time.Second)
	_ = os.Chtimes(key, later, later)
	if got, err := get(t, client, ts.URL); err != nil || got != "bob" {
		t.Fatalf("after the file came back: %q %v", got, err)
	}
}

func TestClientCertificateOptionsAreCheckedTogether(t *testing.T) {
	pki := newPKI(t)
	cert, key := pki.issue(t, "alice", 2, true)
	for name, o := range map[string]Options{
		"cert without key": {CertFile: cert},
		"key without cert": {KeyFile: key},
	} {
		if _, err := o.TLSConfig(""); err == nil || !strings.Contains(err.Error(), "together") {
			t.Errorf("%s: err = %v, want a complaint that they go together", name, err)
		}
	}
	// A pair that cannot be loaded is reported when the config is built, so a
	// typo is not found at the first handshake.
	bad := filepath.Join(t.TempDir(), "absent.pem")
	if _, err := (Options{CertFile: bad, KeyFile: bad}).TLSConfig(""); err != nil {
		t.Fatalf("an unreadable pair is a handshake failure, not a build failure: %v", err)
	}
}

func TestClientTLSMapsToOptions(t *testing.T) {
	c := ClientTLS{CAFile: "ca", CertFile: "c", KeyFile: "k"}
	if c.IsZero() {
		t.Fatal("a filled value is not zero")
	}
	if !(ClientTLS{}).IsZero() {
		t.Fatal("the zero value must be zero")
	}
	o := c.Options(Options{Proxy: "http://p"})
	if o.CAFile != "ca" || o.CertFile != "c" || o.KeyFile != "k" || o.Proxy != "http://p" {
		t.Fatalf("options = %+v", o)
	}
	if key := c.Key(); key == "" || key == (ClientTLS{}).Key() {
		t.Fatalf("key %q must be non-empty for a filled value", key)
	}
	if (ClientTLS{}).Key() != "" {
		t.Fatal("the empty identity has the empty key, so other provider types keep their keys")
	}
}
