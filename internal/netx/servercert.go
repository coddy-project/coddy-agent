package netx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"
)

// Modes of a listener that asks for client certificates.
const (
	ClientAuthOptional = "optional"
	ClientAuthRequired = "required"
)

// ClientCertTLS builds the server side of client certificates for a listener: the pool read from caFile and the mode,
// optional (verify a certificate when one is offered, let a peer without one in) or required (refuse a peer without a
// verified one at the handshake). key names the config key of the CA file in the errors. It returns nil when caFile is
// empty: the listener then asks for no certificate. The server certificate is the caller's. The CA is startup state:
// changing it takes a restart.
func ClientCertTLS(caFile, mode, key string) (*tls.Config, error) {
	if strings.TrimSpace(caFile) == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s %q holds no PEM certificate", key, caFile)
	}
	auth := tls.VerifyClientCertIfGiven
	if mode == ClientAuthRequired {
		auth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ClientCAs: pool, ClientAuth: auth}, nil
}

// CertificateNames are the DNS and URI names of the verified client certificate of a connection, or nil when there is
// none: no TLS, no certificate offered, or a leaf that is not valid at now (checked per request, since a connection
// outlives a certificate). The CN is not consulted: the chain was verified against the listener's client CA at the
// handshake, and a connection that offered none has no verified chain here.
func CertificateNames(state *tls.ConnectionState, now time.Time) []string {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil
	}
	leaf := state.VerifiedChains[0][0]
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil
	}
	out := make([]string, 0, len(leaf.DNSNames)+len(leaf.URIs))
	out = append(out, leaf.DNSNames...)
	for _, u := range leaf.URIs {
		out = append(out, u.String())
	}
	return out
}
