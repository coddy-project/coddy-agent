package config

import (
	"strings"
	"testing"
)

// TLS is the transport's and HTTP's business, not the application's (docs/plans/remote-model-provider-tls-builtin.md): the
// application does not read the identity of a certificate, so there is no key that maps a certificate name to a credential, a class
// or a budget, and no mode that verifies a certificate it then ignores. Each of these keys was one of those and is gone; the schema
// of `coddy -t` says so at the key.
func TestTheApplicationReadsNoCertificateIdentity(t *testing.T) {
	cases := []struct {
		name string
		src  string
		key  string
	}{
		{"shared models cert_names", "httpserver:\n  shared_models:\n    cert_names: [a.example]\n", "cert_names"},
		{"relay client cert_names", "swarm:\n  clients:\n    - name: a\n      token: t\n      scope: shared_models\n      nodes: [\"*\"]\n      cert_names: [a.example]\n", "cert_names"},
		{"httpserver client_auth", "httpserver:\n  tls:\n    cert_file: c.crt\n    key_file: c.key\n    client_ca_file: ca.pem\n    client_auth: required\n", "client_auth"},
		{"swarm client_auth", "swarm:\n  tls:\n    cert_file: c.crt\n    key_file: c.key\n    client_ca_file: ca.pem\n    client_auth: required\n", "client_auth"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := checkYAML(t, withModeline(c.src))
			for _, f := range errorsOf(rep) {
				if strings.Contains(f.Message, c.key) {
					return
				}
			}
			t.Fatalf("`coddy -t` did not refuse the removed key %q; findings: %+v", c.key, rep.Findings)
		})
	}
}

// The keys that stay are transport level: the listener's certificate, the CA that admits a peer at the handshake, and what a client
// trusts and presents. None of them is reported as unknown.
func TestTransportLevelTLSKeysStay(t *testing.T) {
	src := "httpserver:\n  tls:\n    cert_file: c.crt\n    key_file: c.key\n    client_ca_file: ca.pem\n" +
		"swarm:\n  tls:\n    cert_file: c.crt\n    key_file: c.key\n    client_ca_file: ca.pem\n" +
		"  node_tls:\n    ca_file: ca.pem\n    cert_file: c.crt\n    key_file: c.key\n"
	rep := checkYAML(t, withModeline(src))
	for _, f := range rep.Findings {
		for _, key := range []string{"cert_file", "key_file", "client_ca_file", "ca_file", "node_tls", "tls"} {
			if strings.Contains(f.Message, "unknown") && strings.Contains(f.Message, key) {
				t.Fatalf("a transport-level key was refused: %+v", f)
			}
		}
	}
}

// A client CA on a listener means one thing: the handshake requires a certificate that chains to it. There is no second mode.
func TestAClientCAMeansTheHandshakeRequiresACertificate(t *testing.T) {
	if (SwarmTLSConfig{CertFile: "c", KeyFile: "k", ClientCAFile: "ca"}).validate("swarm.tls") != nil {
		t.Fatal("a certificate, a key and a client CA are a valid listener block")
	}
	if err := (SwarmTLSConfig{ClientCAFile: "ca"}).validate("swarm.tls"); err == nil || !strings.Contains(err.Error(), "needs swarm.tls.cert_file") {
		t.Fatalf("a client CA without a server certificate must be refused, got %v", err)
	}
}
