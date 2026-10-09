// Package pki is the built-in public key infrastructure of Coddy: the certificates TLS needs, made with the standard library alone
// (crypto/x509, crypto/ecdsa) at installation, at update and on demand (`coddy tls`), under $CODDY_HOME/tls.
//
// TLS is the transport's business and nothing else (docs/plans/remote-model-provider-tls-builtin.md): this package makes and renews the
// material a listener presents and a client trusts and presents. Coddy reads no identity out of a certificate, so nothing here decides
// what a certificate holder may do.
//
// The package has two halves. Plan is a pure function from what is on disk (parsed, never trusted) and what is wanted to the list of
// steps that would make the state right; Ensure applies that list atomically under a file lock. The model p7-pki-plan holds the pair:
// a valid pair is never replaced by an invalid one, no leaf is left signed by a CA that is gone, the CA key is replaced only by an
// explicit force or when the CA has expired, and a second run does nothing.
package pki

import (
	"crypto/x509"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// The defaults of Want.
const (
	// DefaultCAValidity is the life of the CA: ten years. A CA is replaced only when it has expired or when the operator forces it, and
	// every peer that trusts it must then be told.
	DefaultCAValidity = 10 * 365 * 24 * time.Hour
	// DefaultLeafValidity is the life of a server or client certificate: one year.
	DefaultLeafValidity = 365 * 24 * time.Hour
	// DefaultRenewBefore is how long before its end a leaf is issued again.
	DefaultRenewBefore = 30 * 24 * time.Hour
	// CAWarnBefore is how long before its end the CA is reported as ending (status and `coddy -t`); it is not renewed by itself.
	CAWarnBefore = 90 * 24 * time.Hour
	// clockSkew is how far in the future a certificate may start before it is called wrong: a leaf issued while the clock was off.
	clockSkew = 5 * time.Minute
)

// Want is what the operator and the configuration ask for.
type Want struct {
	// Host names the machine in the subjects of the CA and of the leaves.
	Host string
	// Names are the DNS names and IP addresses the server certificate must carry (see DefaultNames).
	Names []string
	// CAValidity, LeafValidity and RenewBefore default to the constants above when zero.
	CAValidity, LeafValidity, RenewBefore time.Duration
	// ForceCA makes a new CA even when the present one is valid. Every leaf is then issued again, and every peer must trust the new CA.
	ForceCA bool
	// RenewLeaves issues both leaves again now, whatever their state.
	RenewLeaves bool
}

func (w Want) caValidity() time.Duration {
	if w.CAValidity > 0 {
		return w.CAValidity
	}
	return DefaultCAValidity
}

func (w Want) leafValidity() time.Duration {
	if w.LeafValidity > 0 {
		return w.LeafValidity
	}
	return DefaultLeafValidity
}

func (w Want) renewBefore() time.Duration {
	if w.RenewBefore > 0 {
		return w.RenewBefore
	}
	return DefaultRenewBefore
}

// Leaf is a certificate read from disk with the answer to the one question a certificate alone cannot give: does the key file next to
// it belong to it.
type Leaf struct {
	Cert       *x509.Certificate
	KeyMatches bool
}

// State is what is on disk, parsed. A nil field is a file that is missing or that holds no usable certificate.
type State struct {
	CA      *x509.Certificate
	CAKeyOK bool // ca.key holds the private key of CA
	Server  *Leaf
	Client  *Leaf
	Trusted []*x509.Certificate // the CAs of other machines, in file name order
	Bundle  []byte              // bundle.pem as it is, nil when missing
}

// Action is one step of a plan.
type Action string

// The steps. A plan lists them in the order they must be applied.
const (
	CreateCA    Action = "create-ca"
	RenewCA     Action = "renew-ca"
	IssueServer Action = "issue-server"
	IssueClient Action = "issue-client"
	WriteBundle Action = "write-bundle"
)

// Step is an action with the reason the planner gives for it, which is what `coddy tls ensure` prints.
type Step struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// Plan says what would make the state right for want at now. It is pure: the same state, want and time give the same plan, and a plan
// applied leaves a state whose own plan is empty. A valid CA is never replaced unless want forces it; a leaf is issued again only for
// a reason that names itself.
func Plan(st State, w Want, now time.Time) []Step {
	var steps []Step
	newCA := false
	switch {
	case st.CA == nil:
		steps = append(steps, Step{CreateCA, "there is no CA"})
		newCA = true
	case !st.CAKeyOK:
		steps = append(steps, Step{CreateCA, "ca.key is missing or is not the key of ca.crt"})
		newCA = true
	case w.ForceCA:
		steps = append(steps, Step{RenewCA, "forced: every peer must trust the new CA"})
		newCA = true
	case !now.Before(st.CA.NotAfter):
		steps = append(steps, Step{RenewCA, "the CA has expired on " + st.CA.NotAfter.UTC().Format("2006-01-02") + ": every peer must trust the new CA"})
		newCA = true
	}

	// A leaf is judged against the CA that will exist after the steps above.
	if reason := leafReason(st.Server, st.CA, newCA, w, now, true); reason != "" {
		steps = append(steps, Step{IssueServer, reason})
	}
	if reason := leafReason(st.Client, st.CA, newCA, w, now, false); reason != "" {
		steps = append(steps, Step{IssueClient, reason})
	}
	if newCA {
		steps = append(steps, Step{WriteBundle, "the CA is new"})
	} else if !equalBytes(st.Bundle, BundlePEM(st.CA, st.Trusted)) {
		steps = append(steps, Step{WriteBundle, "bundle.pem does not hold this CA and the trusted ones"})
	}
	return steps
}

// leafReason is why a leaf must be issued again, or "".
func leafReason(l *Leaf, ca *x509.Certificate, newCA bool, w Want, now time.Time, server bool) string {
	switch {
	case l == nil || l.Cert == nil:
		return "it is missing or holds no usable certificate"
	case !l.KeyMatches:
		return "the key file is not the key of the certificate"
	case newCA:
		return "the CA is new"
	case w.RenewLeaves:
		return "asked for"
	case ca == nil || l.Cert.CheckSignatureFrom(ca) != nil || l.Cert.Issuer.String() != ca.Subject.String():
		return "it is not signed by this CA"
	case !now.Before(l.Cert.NotAfter):
		return "it has expired on " + l.Cert.NotAfter.UTC().Format("2006-01-02")
	case l.Cert.NotAfter.Sub(now) < w.renewBefore():
		return "it ends on " + l.Cert.NotAfter.UTC().Format("2006-01-02") + ", within " + days(w.renewBefore()) + " days"
	case l.Cert.NotBefore.After(now.Add(clockSkew)):
		return "it starts in the future: it was issued while the clock was wrong"
	}
	if server {
		if !hasUsage(l.Cert, x509.ExtKeyUsageServerAuth) {
			return "it is not a server certificate"
		}
		if missing := MissingNames(l.Cert, w.Names); len(missing) > 0 {
			return "it lacks the names " + strings.Join(missing, ", ")
		}
	} else if !hasUsage(l.Cert, x509.ExtKeyUsageClientAuth) {
		return "it is not a client certificate"
	}
	return ""
}

func hasUsage(c *x509.Certificate, u x509.ExtKeyUsage) bool {
	for _, have := range c.ExtKeyUsage {
		if have == u || have == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func days(d time.Duration) string { return fmt.Sprint(int(d / (24 * time.Hour))) }

// MissingNames are the wanted names (DNS names and IP addresses) that the certificate does not carry. A name the certificate carries and
// nobody wants is harmless and is not a reason to issue it again: the interfaces of a machine come and go.
func MissingNames(c *x509.Certificate, wanted []string) []string {
	have := map[string]bool{}
	for _, d := range c.DNSNames {
		have[strings.ToLower(d)] = true
	}
	for _, ip := range c.IPAddresses {
		have[ip.String()] = true
	}
	var missing []string
	for _, n := range NormalizeNames(wanted) {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// NormalizeNames lower-cases DNS names, writes IP addresses in their canonical form, drops blanks and duplicates and sorts.
func NormalizeNames(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if ip := net.ParseIP(strings.Trim(n, "[]")); ip != nil {
			n = ip.String()
		} else {
			n = strings.ToLower(n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func equalBytes(a, b []byte) bool { return string(a) == string(b) }
