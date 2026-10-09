//go:build http

package httpserver

import (
	"crypto/tls"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// listenerTLS is the TLS configuration of the direct listener's client certificates, or nil when httpserver.tls.client_ca_file
// is empty and the listener asks for none. With a CA the handshake requires a certificate that chains to it; nothing is read out of it. The server certificate is the caller's (ListenAndServeTLS reads cert_file and
// key_file). Startup state: the CA changes with a restart.
func listenerTLS(t config.HTTPTLSConfig) (*tls.Config, error) {
	return netx.ClientCertTLS(t.ClientCAFile, "httpserver.tls.client_ca_file")
}
