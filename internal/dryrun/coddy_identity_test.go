package dryrun

// --dry-run over a coddy row that carries a TLS identity: the certificate pair
// is loaded, its expiry is reported, and the listing probe presents it.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type dryPKI struct {
	dir  string
	ca   *x509.Certificate
	key  *ecdsa.PrivateKey
	file string
}

func newDryPKI(t *testing.T) *dryPKI {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ca, _ := x509.ParseCertificate(der)
	p := &dryPKI{dir: t.TempDir(), ca: ca, key: key}
	p.file = filepath.Join(p.dir, "ca.pem")
	if err := os.WriteFile(p.file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *dryPKI) leaf(t *testing.T, name string, server bool, notAfter time.Time) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile = filepath.Join(p.dir, name+".crt"), filepath.Join(p.dir, name+".key")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile
}

func (p *dryPKI) remote(t *testing.T, seenCN *string) *httptest.Server {
	t.Helper()
	cert, key := p.leaf(t, "remote", true, time.Now().Add(24*time.Hour))
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) > 0 {
			*seenCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		listing("coder")(w, r)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func identityYAML(base, ca, cert, key string) string {
	return fmt.Sprintf("providers:\n  - name: remote\n    type: coddy\n    api_base: %s\n    api_key: shared-token\n    proxy: none\n    ca_file: %s\n    client_cert_file: %s\n    client_key_file: %s\nmodels:\n  - model: remote/coder\nagent:\n  model: remote/coder\n",
		base, ca, cert, key)
}

func TestCoddyIdentityIsPresentedByTheListingProbeAndItsPairIsChecked(t *testing.T) {
	pki := newDryPKI(t)
	var cn string
	srv := pki.remote(t, &cn)
	cert, key := pki.leaf(t, "alice", false, time.Now().Add(90*24*time.Hour))
	rep := run(t, identityYAML(srv.URL, pki.file, cert, key), nil)
	if cn != "alice" {
		t.Fatalf("the remote saw client %q, want alice", cn)
	}
	if c := find(t, rep, "providers[remote]"); c.Status != StatusOK {
		t.Fatalf("listing probe: %+v", c)
	}
	if c := find(t, rep, "providers[remote].client_cert_file"); c.Status != StatusOK || !strings.Contains(c.Message, "expires") {
		t.Fatalf("pair check: %+v", c)
	}
}

func TestCoddyIdentityExpiry(t *testing.T) {
	pki := newDryPKI(t)
	var cn string
	srv := pki.remote(t, &cn)

	soon, soonKey := pki.leaf(t, "soon", false, time.Now().Add(5*24*time.Hour))
	c := find(t, run(t, identityYAML(srv.URL, pki.file, soon, soonKey), nil), "providers[remote].client_cert_file")
	if c.Status != StatusWarning || !strings.Contains(c.Message, "expires") || c.Fix == "" || c.Line == 0 {
		t.Fatalf("a certificate expiring in 5 days: %+v", c)
	}

	gone, goneKey := pki.leaf(t, "gone", false, time.Now().Add(-time.Minute))
	c = find(t, run(t, identityYAML(srv.URL, pki.file, gone, goneKey), nil), "providers[remote].client_cert_file")
	if c.Status != StatusError || !strings.Contains(c.Message, "expired") {
		t.Fatalf("an expired certificate: %+v", c)
	}
}

// The static check refuses a pair that does not load, so a pair that goes bad
// after it (a file swapped or removed between the check and the probe) is what
// the probe sees.
func TestCoddyIdentityThatBreaksAfterTheStaticCheck(t *testing.T) {
	pki := newDryPKI(t)
	var cn string
	srv := pki.remote(t, &cn)
	cert, key := pki.leaf(t, "alice", false, time.Now().Add(90*24*time.Hour))
	prep, _ := prepare(t, identityYAML(srv.URL, pki.file, cert, key))
	req := func() Request { return Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator} }

	_, otherKey := pki.leaf(t, "b", false, time.Now().Add(90*24*time.Hour))
	data, _ := os.ReadFile(otherKey)
	if err := os.WriteFile(key, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c := find(t, Run(context.Background(), req()), "providers[remote].client_cert_file")
	if c.Status != StatusError || c.Fix == "" {
		t.Fatalf("a mismatched pair: %+v", c)
	}
	rep := Run(context.Background(), req())
	for _, ck := range rep.Checks {
		if ck.Path == "providers[remote]" {
			t.Fatalf("the listing was probed with an identity that cannot be used: %+v", ck)
		}
	}

	if err := os.Remove(cert); err != nil {
		t.Fatal(err)
	}
	c = find(t, Run(context.Background(), req()), "providers[remote].client_cert_file")
	if c.Status != StatusError || !strings.Contains(c.Message, "alice.crt") {
		t.Fatalf("a missing file: %+v", c)
	}
}

func TestCoddyRowWithoutIdentityHasNoPairCheck(t *testing.T) {
	srv := sharedRemote(t, listing("coder"))
	rep := run(t, coddyYAML(srv.URL, "coder"), nil)
	for _, c := range rep.Checks {
		if strings.HasSuffix(c.Path, ".client_cert_file") {
			t.Fatalf("unexpected pair check: %+v", c)
		}
	}
	_ = llm.CoddyProtocol
}
