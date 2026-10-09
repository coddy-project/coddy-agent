//go:build http

package httpserver

import (
	"crypto/tls"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// listenerTLS is the TLS configuration of the direct listener's client certificates, or nil when there is no client CA (httpserver.tls.client_ca_file,
// or the built-in bundle under require_client_cert) and the listener asks for none. With a CA the handshake requires a certificate
// that chains to it; nothing is read out of it. The server certificate is the caller's (ListenAndServeTLS reads the pair). Startup state: the CA changes with a restart.
func listenerTLS(clientCAFile string) (*tls.Config, error) {
	return netx.ClientCertTLS(clientCAFile, "httpserver.tls.client_ca_file")
}
