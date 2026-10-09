package pki

import (
	"crypto/x509"
	"sort"
	"strconv"
	"time"
)

// CertStatus describes one certificate for `coddy tls status`, `coddy -t` and `--dry-run`.
type CertStatus struct {
	Present     bool      `json:"present"`
	KeyMatches  bool      `json:"key_matches"`
	Subject     string    `json:"subject,omitempty"`
	NotAfter    time.Time `json:"not_after,omitempty"`
	DaysLeft    int       `json:"days_left"` // negative once it has ended
	Names       []string  `json:"names,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

// Status is the whole directory, described.
type Status struct {
	Dir     string       `json:"dir"`
	CA      CertStatus   `json:"ca"`
	CAKeyOK bool         `json:"ca_key_ok"`
	Server  CertStatus   `json:"server"`
	Client  CertStatus   `json:"client"`
	Trusted []CertStatus `json:"trusted,omitempty"`
	// BundleOK reports that bundle.pem holds exactly this CA and the trusted ones.
	BundleOK bool `json:"bundle_ok"`
	// Pending is what `coddy tls ensure` would do now.
	Pending []Step `json:"pending,omitempty"`
}

// Describe reads dir and says what is there and what ensure would do, for want at now. It writes nothing.
func Describe(dir string, w Want, now time.Time) Status {
	st := Load(dir)
	s := Status{Dir: dir, CAKeyOK: st.CAKeyOK, Pending: Plan(st, w, now)}
	s.CA = certStatus(st.CA, st.CAKeyOK, now)
	if st.Server != nil {
		s.Server = certStatus(st.Server.Cert, st.Server.KeyMatches, now)
	}
	if st.Client != nil {
		s.Client = certStatus(st.Client.Cert, st.Client.KeyMatches, now)
	}
	for _, t := range st.Trusted {
		s.Trusted = append(s.Trusted, certStatus(t, true, now))
	}
	s.BundleOK = st.CA != nil && string(st.Bundle) == string(BundlePEM(st.CA, st.Trusted))
	return s
}

func certStatus(c *x509.Certificate, keyOK bool, now time.Time) CertStatus {
	if c == nil {
		return CertStatus{}
	}
	names := append([]string(nil), c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	sort.Strings(names)
	return CertStatus{
		Present: true, KeyMatches: keyOK, Subject: c.Subject.String(), NotAfter: c.NotAfter,
		DaysLeft: int(c.NotAfter.Sub(now).Hours() / 24), Names: names, Fingerprint: Fingerprint(c),
	}
}

// Findings are the problems of a status a person should hear about, as plain sentences: what is missing, what has ended or ends soon
// (the CA within CAWarnBefore, a leaf within the renewal window) and which key file is wrong. It is what `coddy -t` and `--dry-run` print
// for a configuration that asks for the built-in mode.
func (s Status) Findings(renewBefore time.Duration) []string {
	if renewBefore <= 0 {
		renewBefore = DefaultRenewBefore
	}
	var out []string
	if !s.CA.Present {
		out = append(out, "there is no built-in CA in "+s.Dir+": run `coddy tls ensure`")
		return out
	}
	if !s.CAKeyOK {
		out = append(out, "ca.key is missing or is not the key of ca.crt: run `coddy tls ensure --force-ca` (every peer must then trust the new CA)")
	}
	if s.CA.DaysLeft < 0 {
		out = append(out, "the CA has expired: run `coddy tls ensure` (every peer must then trust the new CA)")
	} else if time.Duration(s.CA.DaysLeft)*24*time.Hour < CAWarnBefore {
		out = append(out, "the CA ends in "+strconv.Itoa(s.CA.DaysLeft)+" days: plan its replacement, every peer must trust the new one")
	}
	for name, c := range map[string]CertStatus{"server certificate": s.Server, "client certificate": s.Client} {
		switch {
		case !c.Present:
			out = append(out, "the "+name+" is missing: run `coddy tls ensure`")
		case !c.KeyMatches:
			out = append(out, "the key of the "+name+" does not match it: run `coddy tls ensure`")
		case c.DaysLeft < 0:
			out = append(out, "the "+name+" has expired: run `coddy tls ensure`")
		case time.Duration(c.DaysLeft)*24*time.Hour < renewBefore:
			out = append(out, "the "+name+" ends in "+strconv.Itoa(c.DaysLeft)+" days: `coddy tls ensure` issues it again")
		}
	}
	sort.Strings(out)
	if !s.BundleOK {
		out = append(out, "bundle.pem does not hold this CA and the trusted ones: run `coddy tls ensure`")
	}
	return out
}
