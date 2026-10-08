//go:build swarm

package swarm

// The relay's own leg to a node that registered itself over a direct https address (swarm.node_tls): the authority the node's
// certificate is verified against and the client certificate the relay presents when the node asks for one.

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// tlsNode is a node that demands a verified client certificate and records the names it saw.
type tlsNode struct {
	ts    *httptest.Server
	mu    sync.Mutex
	names [][]string
}

func newTLSNode(t *testing.T, ca *testCA, nodeCA *testCA) *tlsNode {
	t.Helper()
	n := &tlsNode{}
	n.ts = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var names []string
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			names = r.TLS.VerifiedChains[0][0].DNSNames
		}
		n.mu.Lock()
		n.names = append(n.names, names)
		n.mu.Unlock()
		_, _ = io.WriteString(w, "node answer")
	}))
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{nodeCA.leaf(t, true, nil, time.Now().Add(time.Hour))},
		ClientCAs:    ca.pool(),
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	n.ts.TLS = cfg
	n.ts.StartTLS()
	t.Cleanup(n.ts.Close)
	return n
}

func (n *tlsNode) seen() [][]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([][]string(nil), n.names...)
}

func relayWithNodeTLS(t *testing.T, nodeTLS config.SwarmNodeTLSConfig) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.NodeTLS = nodeTLS
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func writePair(t *testing.T, cert tls.Certificate) (string, string) {
	t.Helper()
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	certPEM, keyPEM := encodeLeaf(t, cert)
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestTheRelayPresentsItsCertificateToASelfRegisteredNode(t *testing.T) {
	clientCA, serverCA := newTestCA(t), newTestCA(t)
	node := newTLSNode(t, clientCA, serverCA)
	certFile, keyFile := writePair(t, clientCA.leaf(t, false, []string{"relay.example"}, time.Now().Add(time.Hour)))
	srv, ts := relayWithNodeTLS(t, config.SwarmNodeTLSConfig{CAFile: serverCA.file, CertFile: certFile, KeyFile: keyFile})
	first, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: "nas02", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: node.ts.URL, InstanceUUID: "uuid", Token: "node-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body := get(t, http.DefaultClient, ts.URL, "/swarm/nodes/nas02/coddy/llm/models", fullToken)
	if code != http.StatusOK || body != "node answer" {
		t.Fatalf("through the mount: %d %q", code, body)
	}
	if seen := node.seen(); len(seen) != 1 || len(seen[0]) != 1 || seen[0][0] != "relay.example" {
		t.Fatalf("the node saw %v, want one request carrying relay.example", seen)
	}
	// A renewal by the owner keeps the identity.
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: "nas02", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: node.ts.URL, InstanceUUID: "uuid", Token: "node-secret", LeaseSecret: first.LeaseSecret,
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, http.DefaultClient, ts.URL, "/swarm/nodes/nas02/coddy/llm/models", fullToken); code != http.StatusOK {
		t.Errorf("after a renewal: %d", code)
	}
}

func TestWithoutNodeTLSTheNodeRefusesTheRelay(t *testing.T) {
	clientCA, serverCA := newTestCA(t), newTestCA(t)
	node := newTLSNode(t, clientCA, serverCA)
	srv, ts := relayWithNodeTLS(t, config.SwarmNodeTLSConfig{CAFile: serverCA.file})
	if _, err := srv.registry.Register(swarmdto.RegisterRequest{
		Name: "nas02", Kind: swarmdto.KindAgent, Transport: swarmdto.TransportDirect,
		AdvertiseURL: node.ts.URL, InstanceUUID: "uuid", Token: "node-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, http.DefaultClient, ts.URL, "/swarm/nodes/nas02/coddy/llm/models", fullToken); code != http.StatusBadGateway {
		t.Fatalf("a node that wants a certificate, a relay that has none: %d, want 502", code)
	}
	if len(node.seen()) != 0 {
		t.Error("the node served a request without a verified certificate")
	}
}

func TestAnUpstreamWithItsOwnDialKeepsIt(t *testing.T) {
	clientCA, serverCA := newTestCA(t), newTestCA(t)
	node := newTLSNode(t, clientCA, serverCA)
	ownCert, ownKey := writePair(t, clientCA.leaf(t, false, []string{"upstream.example"}, time.Now().Add(time.Hour)))
	relayCert, relayKey := writePair(t, clientCA.leaf(t, false, []string{"relay.example"}, time.Now().Add(time.Hour)))
	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = fullToken
	cfg.Swarm.NodeTLS = config.SwarmNodeTLSConfig{CAFile: serverCA.file, CertFile: relayCert, KeyFile: relayKey}
	cfg.Swarm.Upstreams = []config.SwarmUpstream{{Name: "pinned", URL: node.ts.URL, Token: "t",
		Dial: config.SwarmDialConfig{CAFile: serverCA.file, CertFile: ownCert, KeyFile: ownKey}}}
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if code, _ := get(t, http.DefaultClient, ts.URL, "/swarm/nodes/pinned/coddy/llm/models", fullToken); code != http.StatusOK {
		t.Fatalf("%d", code)
	}
	if seen := node.seen(); len(seen) != 1 || seen[0][0] != "upstream.example" {
		t.Fatalf("the node saw %v, want upstream.example", seen)
	}
}

// encodeLeaf writes a test certificate and its ECDSA key as PEM.
func encodeLeaf(t *testing.T, cert tls.Certificate) ([]byte, []byte) {
	t.Helper()
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("the test key is %T", cert.PrivateKey)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
