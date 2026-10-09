package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// newKey makes an ECDSA P-256 key: small, fast and accepted by every TLS stack Coddy talks to.
func newKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// serial is a random 127-bit positive serial number (RFC 5280 asks for up to 20 octets, unique per issuer).
func serial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("serial number: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}

// newCA makes a self-signed CA that can sign leaves and nothing below them (a path length of zero).
func newCA(host string, now time.Time, validity time.Duration) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: "Coddy local CA (" + host + ")", Organization: []string{"Coddy"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("make the CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// newLeaf makes a certificate signed by the CA. A server leaf carries names (DNS names and IP addresses) and serverAuth; a client
// leaf carries the host name and clientAuth. The subject says what the certificate is for and where it was made, and nothing reads it.
func newLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string, names []string, server bool, now time.Time, validity time.Duration) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.Subject = pkix.Name{CommonName: "coddy server " + host, Organization: []string{"Coddy"}}
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, n := range NormalizeNames(names) {
			if ip := net.ParseIP(n); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else {
				tmpl.DNSNames = append(tmpl.DNSNames, n)
			}
		}
	} else {
		tmpl.Subject = pkix.Name{CommonName: "coddy client " + host, Organization: []string{"Coddy"}}
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("make the certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// CertPEM is the PEM of one certificate.
func CertPEM(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}

// KeyPEM is the PEM of an EC private key (SEC 1), which is what every reader of Coddy's files accepts.
func KeyPEM(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// BundlePEM is bundle.pem: this machine's CA first, then the CAs it was told to trust, each once. It is what `ca_file` and
// `client_ca_file` point at in the built-in mode. A nil CA gives only the trusted ones.
func BundlePEM(ca *x509.Certificate, trusted []*x509.Certificate) []byte {
	var out []byte
	seen := map[string]bool{}
	add := func(c *x509.Certificate) {
		if c == nil || seen[string(c.Raw)] {
			return
		}
		seen[string(c.Raw)] = true
		out = append(out, CertPEM(c)...)
	}
	add(ca)
	for _, t := range trusted {
		add(t)
	}
	return out
}
