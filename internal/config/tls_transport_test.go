package config

import (
	"strings"
	"testing"
)

// TLS is the transport's and HTTP's business, not the application's: the
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
