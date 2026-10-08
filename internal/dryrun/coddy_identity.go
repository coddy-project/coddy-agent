package dryrun

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// identityExpiryWarning is how long before NotAfter a client certificate is
// worth a warning: a remote refuses it the moment it expires, and a long-lived
// session finds out at the next handshake.
const identityExpiryWarning = 14 * 24 * time.Hour

// coddyIdentityChecks loads the TLS identity of a coddy row the way a request
// would: the authority bundle must hold a certificate, and the client
// certificate and key must be a pair that has not expired. It reports the
// expiry within 14 days as a warning and a past NotAfter as an error, at the
// key of the certificate. A row with no identity yields nothing.
func (r *runner) coddyIdentityChecks(prov *config.ProviderConfig) []Check {
	id := prov.ClientTLS()
	if id.IsZero() {
		return nil
	}
	path := "providers[" + prov.Name + "]"
	var out []Check
	if id.CAFile != "" {
		out = append(out, r.caFileCheck(path+".ca_file", id.CAFile))
	}
	if id.CertFile == "" || id.KeyFile == "" {
		return out
	}
	loc := path + ".client_cert_file"
	pair, err := tls.LoadX509KeyPair(id.CertFile, id.KeyFile)
	if err != nil {
		return append(out, r.check(StatusError, loc, loc, "cannot use the client certificate: "+shortErr(err),
			"client_cert_file and client_key_file must be a readable PEM pair of one certificate and its key"))
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return append(out, r.check(StatusError, loc, loc, "client certificate is not valid X.509: "+shortErr(err), "re-issue the certificate"))
	}
	left := time.Until(leaf.NotAfter)
	switch {
	case left <= 0:
		return append(out, r.check(StatusError, loc, loc,
			fmt.Sprintf("client certificate expired on %s", leaf.NotAfter.UTC().Format("2006-01-02")),
			"renew the certificate; the file is read at each handshake, so no restart is needed"))
	case left < identityExpiryWarning:
		return append(out, r.check(StatusWarning, loc, loc,
			fmt.Sprintf("client certificate expires on %s (in %d days)", leaf.NotAfter.UTC().Format("2006-01-02"), int(left.Hours()/24)),
			"renew the certificate before then; the file is read at each handshake, so no restart is needed"))
	}
	return append(out, r.check(StatusOK, loc, loc,
		fmt.Sprintf("client certificate %q expires on %s", strings.TrimSpace(leaf.Subject.CommonName), leaf.NotAfter.UTC().Format("2006-01-02")), ""))
}

// hasIdentityError reports whether any of the checks says the identity cannot
// be used, in which case the listing probe would only repeat it.
func hasIdentityError(checks []Check) bool {
	for _, c := range checks {
		if c.Status == StatusError {
			return true
		}
	}
	return false
}
