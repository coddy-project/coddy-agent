//go:build http

package httpserver

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// listenerTLS is the TLS configuration of the direct listener's client certificates, or nil when httpserver.tls.client_ca_file
// is empty and the listener asks for none. The server certificate is the caller's (ListenAndServeTLS reads cert_file and
// key_file). Startup state: the CA changes with a restart.
func listenerTLS(t config.HTTPTLSConfig) (*tls.Config, error) {
	return netx.ClientCertTLS(t.ClientCAFile, t.EffectiveClientAuth(), "httpserver.tls.client_ca_file")
}

// sharedCertName is the first name of the request's verified client certificate that the snapshot's
// httpserver.shared_models.cert_names lists, or "". The identity is read per request from the connection (a connection
// outlives a certificate, so the leaf is checked for validity now) against the snapshot's list, so a reload takes effect on
// the next call. The gate asks for it on the shared-model routes (the three calls and the probe's ping) only.
func sharedCertName(r *http.Request, pol *authPolicy) string {
	if pol == nil || len(pol.certNames) == 0 {
		return ""
	}
	for _, have := range netx.CertificateNames(r.TLS, time.Now()) {
		for _, want := range pol.certNames {
			if have == want {
				return have
			}
		}
	}
	return ""
}
