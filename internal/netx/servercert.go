package netx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// ClientCertTLS builds the server side of mutual TLS for a listener: the pool read from caFile, and the handshake then requires a
// certificate that chains to it and refuses a peer without one. That is all it does. TLS is the transport's business: the application
// reads no identity out of the certificate (no name, no subject, no class), so what a certificate holder may do is for the reverse
// proxy and the infrastructure. key names the config key of the CA file in the errors. It returns nil when caFile is empty: the
// listener then asks for no certificate. The server certificate is the caller's. The CA is startup state: changing it takes a restart.
func ClientCertTLS(caFile, key string) (*tls.Config, error) {
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
	return &tls.Config{MinVersion: tls.VersionTLS12, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
