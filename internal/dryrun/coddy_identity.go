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
	id := r.req.Cfg.ProviderClientTLS(prov)
	if id.IsZero() {
		return nil
	}
	path := "providers[" + prov.Name + "]"
	var out []Check
	if id.CAFile != "" {
		out = append(out, r.caFileCheck(path+".ca_file", id.CAFile))
	}
	return append(out, r.clientPairChecks(path+".client_cert_file", id.CertFile, id.KeyFile)...)
}

// clientPairChecks loads a client certificate and its key the way a handshake would and reports the expiry: a pair that cannot be used,
// or is not yet valid or already past, is an error at loc; one that expires within 14 days is a warning. Used for a coddy row's pair, a
// join's and an upstream's dial pair, and the relay's node_tls pair. Nothing is checked when either file is not named.
func (r *runner) clientPairChecks(loc, certFile, keyFile string) []Check {
	return r.pairChecks("client certificate", loc, certFile, keyFile)
}

// serverPairChecks is the same for the pair a listener serves with (httpserver.tls, swarm.tls): the expiry of a server certificate is the
// commonest outage of a TLS deployment, and the pair is read once at start, so a renewal needs a restart the operator has to plan.
func (r *runner) serverPairChecks(loc, certFile, keyFile string) []Check {
	return r.pairChecks("server certificate", loc, certFile, keyFile)
}

func (r *runner) pairChecks(label, loc, certFile, keyFile string) []Check {
	var out []Check
	if certFile == "" || keyFile == "" {
		return out
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return append(out, r.check(StatusError, loc, loc, "cannot use the "+label+": "+shortErr(err),
			"the certificate and the key must be a readable PEM pair of one certificate and its own private key (a key with a passphrase cannot be used)"))
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return append(out, r.check(StatusError, loc, loc, label+" is not valid X.509: "+shortErr(err), "re-issue the certificate"))
	}
	left := time.Until(leaf.NotAfter)
	switch {
	case time.Now().Before(leaf.NotBefore):
		return append(out, r.check(StatusError, loc, loc,
			fmt.Sprintf("%s is not valid before %s", label, leaf.NotBefore.UTC().Format("2006-01-02 15:04")),
			"issue a certificate that is valid now, or wait until it is; the remote refuses it until then"))
	case left <= 0:
		return append(out, r.check(StatusError, loc, loc,
			fmt.Sprintf("%s expired on %s", label, leaf.NotAfter.UTC().Format("2006-01-02")),
			renewalHint(label)))
	case left < identityExpiryWarning:
		return append(out, r.check(StatusWarning, loc, loc,
			fmt.Sprintf("%s expires on %s (in %d days)", label, leaf.NotAfter.UTC().Format("2006-01-02"), int(left.Hours()/24)),
			"renew the certificate before then; "+renewalHint(label)))
	}
	return append(out, r.check(StatusOK, loc, loc,
		fmt.Sprintf("%s %q expires on %s", label, strings.TrimSpace(leaf.Subject.CommonName), leaf.NotAfter.UTC().Format("2006-01-02")), ""))
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

// renewalHint says what a renewal costs: a client pair is read at each handshake, a server pair once, at start.
func renewalHint(label string) string {
	if label == "server certificate" {
		return "the pair is read at start, so a renewal needs a restart of coddy serve"
	}
	return "the file is read at each handshake, so no restart is needed"
}
