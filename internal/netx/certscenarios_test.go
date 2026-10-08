package netx

// The certificate scenarios of docs/operate/certificates.md, run as real TLS handshakes over loopback with the functions Coddy itself
// uses: Options.TLSConfig on the dialling side (the authority it trusts, the pair it presents) and ClientCertTLS plus CertificateNames
// on the serving side (the authority client certificates are verified against, the names that map them). The statements of the guide
// that depend on crypto/x509 and on those two functions are held here, so a change of either breaks the guide's tests, not a reader.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scenarioCert is one certificate with its key, as the scenarios of the guide need them.
type scenarioCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

type certSpec struct {
	cn        string
	dns       []string
	ips       []net.IP
	uris      []string
	emails    []string
	eku       []x509.ExtKeyUsage // nil: no extension at all
	ca        bool
	notBefore time.Time
	notAfter  time.Time
	parent    *scenarioCert // nil: self-signed
}

var scenarioSerial atomic.Int64

func newScenarioCert(t *testing.T, s certSpec) *scenarioCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if s.notBefore.IsZero() {
		s.notBefore = time.Now().Add(-time.Hour)
	}
	if s.notAfter.IsZero() {
		s.notAfter = time.Now().Add(24 * time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(scenarioSerial.Add(1)), Subject: pkix.Name{CommonName: s.cn},
		DNSNames: s.dns, IPAddresses: s.ips, EmailAddresses: s.emails,
		NotBefore: s.notBefore, NotAfter: s.notAfter, ExtKeyUsage: s.eku,
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	for _, u := range s.uris {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, parsed)
	}
	if s.ca {
		tmpl.IsCA, tmpl.BasicConstraintsValid, tmpl.KeyUsage = true, true, x509.KeyUsageCertSign|x509.KeyUsageCRLSign
	}
	parent, signer := tmpl, key
	if s.parent != nil {
		parent, signer = s.parent.cert, s.parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &scenarioCert{cert: cert, key: key, der: der}
}

func (c *scenarioCert) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der})
}

// files writes the certificate (with the given chain after it, as a fullchain.pem) and its key, and returns the two paths.
func (c *scenarioCert) files(t *testing.T, dir, name string, chain ...*scenarioCert) (certPath, keyPath string) {
	t.Helper()
	body := c.pem()
	for _, p := range chain {
		body = append(body, p.pem()...)
	}
	certPath, keyPath = filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	if err := os.WriteFile(certPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(c.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func writePEM(t *testing.T, dir, name string, certs ...*scenarioCert) string {
	t.Helper()
	var body []byte
	for _, c := range certs {
		body = append(body, c.pem()...)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// scenarioServer is a TLS listener the way coddy serve builds one: its own pair, and the client authority and mode of ClientCertTLS.
// Each request answers with the names the client certificate mapped to, read the way the gate reads them.
type scenarioServer struct {
	ts *httptest.Server
}

func startScenarioServer(t *testing.T, serverPair tls.Certificate, clientCAFile, mode string) *scenarioServer {
	t.Helper()
	cfg, err := ClientCertTLS(clientCAFile, mode, "test.client_ca_file")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cfg.Certificates = []tls.Certificate{serverPair}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Join(CertificateNames(r.TLS, time.Now()), ",")))
	}))
	ts.TLS = cfg
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return &scenarioServer{ts: ts}
}

// pairOf writes a certificate and its key under dir and loads them as a serving pair.
func pairOf(t *testing.T, c *scenarioCert, dir, name string, chain ...*scenarioCert) tls.Certificate {
	t.Helper()
	certPath, keyPath := c.files(t, dir, name, chain...)
	return loadPair(t, certPath, keyPath)
}

func loadPair(t *testing.T, certPath, keyPath string) tls.Certificate {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// get dials the server with the settings of one outbound leg, as a coddy row, a join or a relay does, and returns the names the server
// mapped the presented certificate to, or the error of the handshake.
func (s *scenarioServer) get(t *testing.T, opts Options, host string) (string, error) {
	t.Helper()
	u, _ := url.Parse(s.ts.URL)
	cfg, err := opts.TLSConfig(host)
	if err != nil {
		return "", err
	}
	tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	res, err := c.Get("https://" + net.JoinHostPort(u.Hostname(), u.Port()) + "/")
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	return string(buf[:n]), nil
}

func serverCertFor(t *testing.T, ca *scenarioCert, names ...string) *scenarioCert {
	t.Helper()
	return newScenarioCert(t, certSpec{cn: "relay", dns: names, ips: []net.IP{net.ParseIP("127.0.0.1")},
		eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, parent: ca})
}

// A self-signed CLIENT certificate imported to the relay: the relay's client_ca_file holds the certificate itself, which is its own anchor.
func TestScenarioSelfSignedClientCertificateIsItsOwnAnchor(t *testing.T) {
	dir := t.TempDir()
	serverCA := newScenarioCert(t, certSpec{cn: "server ca", ca: true})
	srvPair := pairOf(t, serverCertFor(t, serverCA, "relay.example"), dir, "srv")
	// The client certificate is self-signed: no CA, a DNS name that cert_names would list, the clientAuth key usage.
	alice := newScenarioCert(t, certSpec{cn: "alice", dns: []string{"alice.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	other := newScenarioCert(t, certSpec{cn: "mallory", dns: []string{"alice.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	srv := startScenarioServer(t, srvPair, writePEM(t, dir, "clients.pem", alice), ClientAuthRequired)
	trust := writePEM(t, dir, "server-ca.pem", serverCA)

	aliceCert, aliceKey := alice.files(t, dir, "alice")
	got, err := srv.get(t, Options{CAFile: trust, CertFile: aliceCert, KeyFile: aliceKey}, "relay.example")
	if err != nil || got != "alice.example" {
		t.Fatalf("the imported self-signed certificate: %q %v", got, err)
	}
	// A different self-signed certificate with the very same name is not that certificate: the name proves nothing without the anchor.
	otherCert, otherKey := other.files(t, dir, "mallory")
	if got, err := srv.get(t, Options{CAFile: trust, CertFile: otherCert, KeyFile: otherKey}, "relay.example"); err == nil {
		t.Fatalf("a self-signed certificate nobody imported was accepted: %q", got)
	}
	// And required means required: no certificate, no handshake.
	if got, err := srv.get(t, Options{CAFile: trust}, "relay.example"); err == nil {
		t.Fatalf("a peer with no certificate was served under required: %q", got)
	}
}

// A self-signed SERVER certificate trusted by its clients through ca_file: the file holds the certificate itself.
func TestScenarioSelfSignedServerCertificateTrustedByCAFile(t *testing.T) {
	dir := t.TempDir()
	relay := newScenarioCert(t, certSpec{cn: "relay", dns: []string{"relay.example"}, ips: []net.IP{net.ParseIP("127.0.0.1")},
		eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	srv := startScenarioServer(t, pairOf(t, relay, dir, "srv"), "", "")
	trust := writePEM(t, dir, "relay-self.pem", relay)

	if got, err := srv.get(t, Options{CAFile: trust}, "relay.example"); err != nil {
		t.Fatalf("a client that trusts the certificate itself: %q %v", got, err)
	}
	// The host name is checked against the names in the certificate (never the CN); an address needs an IP SAN, which this one has.
	if _, err := srv.get(t, Options{CAFile: trust}, "127.0.0.1"); err != nil {
		t.Errorf("an IP address named in the certificate: %v", err)
	}
	if _, err := srv.get(t, Options{CAFile: trust}, "other.example"); err == nil || !strings.Contains(err.Error(), "other.example") {
		t.Errorf("a name the certificate does not carry must fail on the name: %v", err)
	}
	// Without the file the client falls back to the system roots, which do not know this certificate.
	if _, err := srv.get(t, Options{}, "relay.example"); err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Errorf("a self-signed server certificate with no ca_file: %v", err)
	}
	// ca_file REPLACES the system roots for the leg: a bundle that holds another authority trusts nothing else.
	elsewhere := newScenarioCert(t, certSpec{cn: "elsewhere", ca: true})
	if _, err := srv.get(t, Options{CAFile: writePEM(t, dir, "elsewhere.pem", elsewhere)}, "relay.example"); err == nil {
		t.Error("a ca_file of another authority verified the relay")
	}
}

// The relay (or any operator) as the private CA of its clients: one authority issues the server certificate and every client certificate,
// and the clients are told apart by the names in cert_names.
func TestScenarioPrivateCAIssuesServerAndClients(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "coddy private ca", ca: true})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, ca, "relay.example"), dir, "srv"),
		writePEM(t, dir, "ca.pem", ca), ClientAuthOptional)
	trust := writePEM(t, dir, "ca.pem", ca)
	clientEKU := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	for _, c := range []struct {
		name string
		spec certSpec
		want string
	}{
		{"a DNS name", certSpec{cn: "a", dns: []string{"acme.example"}, eku: clientEKU, parent: ca}, "acme.example"},
		{"a URI name", certSpec{cn: "b", uris: []string{"urn:coddy:client:acme"}, eku: clientEKU, parent: ca}, "urn:coddy:client:acme"},
		{"no extended key usage at all", certSpec{cn: "c", dns: []string{"plain.example"}, parent: ca}, "plain.example"},
		{"both key usages", certSpec{cn: "d", dns: []string{"both.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, parent: ca}, "both.example"},
	} {
		certPath, keyPath := newScenarioCert(t, c.spec).files(t, dir, strings.ReplaceAll(c.name, " ", "-"))
		got, err := srv.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example")
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want the name %q", c.name, got, err, c.want)
		}
	}
	// Optional mode: a peer with no certificate is let in (a browser, a bearer client) and has no name.
	if got, err := srv.get(t, Options{CAFile: trust}, "relay.example"); err != nil || got != "" {
		t.Errorf("no certificate under optional: %q %v", got, err)
	}
}

// A chain with an intermediate: the leaf file carries the intermediate after it (fullchain), the client_ca_file holds the root.
func TestScenarioIntermediateAuthority(t *testing.T) {
	dir := t.TempDir()
	root := newScenarioCert(t, certSpec{cn: "root", ca: true})
	inter := newScenarioCert(t, certSpec{cn: "issuing", ca: true, parent: root})
	leaf := newScenarioCert(t, certSpec{cn: "node", dns: []string{"node.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, parent: inter})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, root, "relay.example"), dir, "srv"),
		writePEM(t, dir, "root.pem", root), ClientAuthRequired)
	trust := writePEM(t, dir, "root.pem", root)

	withChain, keyPath := leaf.files(t, dir, "with-chain", inter)
	if got, err := srv.get(t, Options{CAFile: trust, CertFile: withChain, KeyFile: keyPath}, "relay.example"); err != nil || got != "node.example" {
		t.Fatalf("a leaf presented with its intermediate: %q %v", got, err)
	}
	alone, keyAlone := leaf.files(t, dir, "alone")
	if _, err := srv.get(t, Options{CAFile: trust, CertFile: alone, KeyFile: keyAlone}, "relay.example"); err == nil {
		t.Error("a leaf presented without its intermediate was accepted: the file must be the full chain")
	}
	// Putting the intermediate into the authority bundle instead also works: the bundle is any set of anchors.
	both := writePEM(t, dir, "inter.pem", inter)
	srv2 := startScenarioServer(t, pairOf(t, serverCertFor(t, root, "relay.example"), dir, "srv2"), both, ClientAuthRequired)
	if got, err := srv2.get(t, Options{CAFile: trust, CertFile: alone, KeyFile: keyAlone}, "relay.example"); err != nil || got != "node.example" {
		t.Errorf("an intermediate as the anchor: %q %v", got, err)
	}
}

// A certificate from a public CA is made for servers: with only the serverAuth key usage it is refused as a client certificate.
func TestScenarioServerOnlyCertificateIsNotAClientCertificate(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "public-like ca", ca: true})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, ca, "relay.example"), dir, "srv"),
		writePEM(t, dir, "ca.pem", ca), ClientAuthRequired)
	trust := writePEM(t, dir, "ca.pem", ca)
	serverOnly := newScenarioCert(t, certSpec{cn: "www", dns: []string{"www.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, parent: ca})
	certPath, keyPath := serverOnly.files(t, dir, "www")
	got, err := srv.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example")
	if err == nil {
		t.Fatalf("a serverAuth-only certificate opened a connection as a client: %q", got)
	}
}

// A wildcard certificate: as the server's certificate it names every host one label below; as a client identity its name is the literal
// string, shared by everyone who holds the key, so it is no way to tell two clients apart.
func TestScenarioWildcardCertificates(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "corp ca", ca: true})
	wild := newScenarioCert(t, certSpec{cn: "*.corp.example", dns: []string{"*.corp.example"}, ips: []net.IP{net.ParseIP("127.0.0.1")},
		eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, parent: ca})
	wildCert, wildKey := wild.files(t, dir, "wild")
	srv := startScenarioServer(t, loadPair(t, wildCert, wildKey), writePEM(t, dir, "ca.pem", ca), ClientAuthOptional)
	trust := writePEM(t, dir, "ca.pem", ca)

	for _, host := range []string{"relay.corp.example", "node.corp.example"} {
		if _, err := srv.get(t, Options{CAFile: trust}, host); err != nil {
			t.Errorf("a wildcard server certificate for %s: %v", host, err)
		}
	}
	for _, host := range []string{"a.b.corp.example", "corp.example", "relay.other.example"} {
		if _, err := srv.get(t, Options{CAFile: trust}, host); err == nil {
			t.Errorf("a wildcard certificate matched %s: it covers one label and not the bare domain", host)
		}
	}
	got, err := srv.get(t, Options{CAFile: trust, CertFile: wildCert, KeyFile: wildKey}, "relay.corp.example")
	if err != nil || got != "*.corp.example" {
		t.Errorf("the same wildcard certificate as a client: %q %v, want the literal name", got, err)
	}
}

// A personal certificate carries an e-mail address (S/MIME) or a user principal name, not a DNS or URI name: the chain is fine, the
// handshake succeeds, and there is no name for cert_names to map.
func TestScenarioPersonalCertificateWithOnlyAnEmailHasNoName(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "corp ca", ca: true})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, ca, "relay.example"), dir, "srv"),
		writePEM(t, dir, "ca.pem", ca), ClientAuthOptional)
	trust := writePEM(t, dir, "ca.pem", ca)
	personal := newScenarioCert(t, certSpec{cn: "Alice Example", emails: []string{"alice@corp.example"},
		eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageEmailProtection}, parent: ca})
	certPath, keyPath := personal.files(t, dir, "alice")
	got, err := srv.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example")
	if err != nil || got != "" {
		t.Fatalf("an e-mail-only certificate: %q %v, want a successful handshake and no name", got, err)
	}
	// The same person with a URI name added by the CA (a template that supplies one) is identified.
	named := newScenarioCert(t, certSpec{cn: "Alice Example", emails: []string{"alice@corp.example"}, uris: []string{"urn:coddy:user:alice"},
		eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, parent: ca})
	certPath, keyPath = named.files(t, dir, "alice-named")
	if got, err := srv.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example"); err != nil || got != "urn:coddy:user:alice" {
		t.Errorf("a personal certificate with a URI name: %q %v", got, err)
	}
}

// A certificate outside its validity is refused at the handshake, and a leaf that expires on an open connection has no name on the next request.
func TestScenarioExpiredAndNotYetValidCertificates(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "ca", ca: true})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, ca, "relay.example"), dir, "srv"),
		writePEM(t, dir, "ca.pem", ca), ClientAuthOptional)
	trust := writePEM(t, dir, "ca.pem", ca)
	for name, spec := range map[string]certSpec{
		"expired":       {cn: "e", dns: []string{"e.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, notBefore: time.Now().Add(-48 * time.Hour), notAfter: time.Now().Add(-time.Hour), parent: ca},
		"not yet valid": {cn: "n", dns: []string{"n.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, notBefore: time.Now().Add(time.Hour), notAfter: time.Now().Add(48 * time.Hour), parent: ca},
	} {
		spec.parent = ca
		certPath, keyPath := newScenarioCert(t, spec).files(t, dir, strings.ReplaceAll(name, " ", "-"))
		if got, err := srv.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example"); err == nil && got != "" {
			t.Errorf("%s certificate gave the name %q", name, got)
		}
	}
}

// A private key protected by a passphrase cannot be used: Coddy reads PEM files and has nowhere to ask for the passphrase. The error is
// the one the guide tells the reader to look for.
func TestScenarioAnEncryptedPrivateKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	c := newScenarioCert(t, certSpec{cn: "c", dns: []string{"c.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	certPath, _ := c.files(t, dir, "c")
	der, err := x509.MarshalECPrivateKey(c.key)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // the legacy PEM encryption is exactly the format a passphrase-protected key from an old openssl has.
	block, err := x509.EncryptPEMBlock(rand.Reader, "EC PRIVATE KEY", der, []byte("hunter2"), x509.PEMCipherAES256)
	if err != nil {
		t.Fatal(err)
	}
	encKey := filepath.Join(dir, "c-enc.key")
	if err := os.WriteFile(encKey, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = (Options{CertFile: certPath, KeyFile: encKey}).TLSConfig("x")
	if err != nil {
		t.Fatalf("the pair is read at the handshake, not when the settings are built: %v", err)
	}
	cfg, _ := (Options{CertFile: certPath, KeyFile: encKey}).TLSConfig("x")
	if _, err := cfg.GetClientCertificate(&tls.CertificateRequestInfo{}); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("an encrypted key must fail with a private key error, got %v", err)
	}
}

// A client pair that is replaced on disk is picked up by the next handshake, with no restart; a server's pair is not (it is startup state).
func TestScenarioClientCertificateRotationNeedsNoRestart(t *testing.T) {
	dir := t.TempDir()
	ca := newScenarioCert(t, certSpec{cn: "ca", ca: true})
	srv := startScenarioServer(t, pairOf(t, serverCertFor(t, ca, "relay.example"), dir, "srv"),
		writePEM(t, dir, "ca.pem", ca), ClientAuthOptional)
	trust := writePEM(t, dir, "ca.pem", ca)
	eku := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	first := newScenarioCert(t, certSpec{cn: "v1", dns: []string{"v1.example"}, eku: eku, parent: ca})
	certPath, keyPath := first.files(t, dir, "client")
	opts := Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}
	cfg, err := opts.TLSConfig("relay.example")
	if err != nil {
		t.Fatal(err)
	}
	dial := func() string {
		tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		res, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(srv.ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		buf := make([]byte, 256)
		n, _ := res.Body.Read(buf)
		return string(buf[:n])
	}
	if got := dial(); got != "v1.example" {
		t.Fatalf("before the rotation: %q", got)
	}
	// Rotate: the renewed pair replaces the files; one TLS configuration, built once, keeps serving.
	time.Sleep(20 * time.Millisecond) // the loader's cache is keyed by size and modification time
	second := newScenarioCert(t, certSpec{cn: "v2", dns: []string{"v2.example-renewed"}, eku: eku, parent: ca})
	second.files(t, dir, "client")
	if got := dial(); got != "v2.example-renewed" {
		t.Fatalf("after the rotation, on the same configuration: %q, want the renewed certificate", got)
	}
}

// An imported self-signed file is an anchor, so whatever it is allowed to sign is accepted too: a file with CA:TRUE (what `openssl req -x509`
// makes unless told otherwise) lets its holder issue a certificate with any name, including one another client is known by. The guide tells
// the operator to import only files that say CA:FALSE.
func TestScenarioAnImportedSelfSignedCAFileAcceptsWhatItSigns(t *testing.T) {
	dir := t.TempDir()
	serverCA := newScenarioCert(t, certSpec{cn: "server ca", ca: true})
	bob := newScenarioCert(t, certSpec{cn: "bob", dns: []string{"bob.example"}, ca: true, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	alice := newScenarioCert(t, certSpec{cn: "alice", dns: []string{"alice.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	bundle := writePEM(t, dir, "clients.pem", alice, bob) // alice imported her certificate, bob imported his: his has CA:TRUE
	server := startScenarioServer(t, pairOf(t, serverCertFor(t, serverCA, "relay.example"), dir, "srv2"), bundle, ClientAuthRequired)
	trust := writePEM(t, dir, "server-ca.pem", serverCA)

	// Bob signs a certificate that carries alice's name.
	forged := newScenarioCert(t, certSpec{cn: "alice", dns: []string{"alice.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, parent: bob})
	certPath, keyPath := forged.files(t, dir, "forged", bob)
	got, err := server.get(t, Options{CAFile: trust, CertFile: certPath, KeyFile: keyPath}, "relay.example")
	if err != nil || got != "alice.example" {
		t.Fatalf("expected the demonstration to hold: a CA:TRUE import accepts a certificate it signed with alice's name: %q %v", got, err)
	}
	// The same attempt against a bundle whose self-signed file says CA:FALSE fails: the leaf is not an authority.
	bobLeaf := newScenarioCert(t, certSpec{cn: "bob", dns: []string{"bob.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	safe := startScenarioServer(t, pairOf(t, serverCertFor(t, serverCA, "relay.example"), dir, "srv3"), writePEM(t, dir, "safe.pem", alice, bobLeaf), ClientAuthRequired)
	// bobLeaf cannot sign (its x509 template has no CertSign), so the same forgery cannot even be made with it as the issuer by a verifier.
	forged2 := newScenarioCert(t, certSpec{cn: "alice", dns: []string{"alice.example"}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, parent: bobLeaf})
	cert2, key2 := forged2.files(t, dir, "forged2", bobLeaf)
	if got, err := safe.get(t, Options{CAFile: trust, CertFile: cert2, KeyFile: key2}, "relay.example"); err == nil {
		t.Fatalf("a certificate signed by an imported CA:FALSE leaf was accepted: %q", got)
	}
}
