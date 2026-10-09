package netx

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ClientTLS is the identity of one outbound leg: the authority it trusts and the
// certificate it presents. It is three paths, never the key material, so it can
// ride a resolved configuration and be compared.
type ClientTLS struct {
	CAFile   string
	CertFile string
	KeyFile  string
}

// IsZero reports an empty identity.
func (c ClientTLS) IsZero() bool {
	return strings.TrimSpace(c.CAFile) == "" && strings.TrimSpace(c.CertFile) == "" && strings.TrimSpace(c.KeyFile) == ""
}

// Options returns base with the identity's three files set.
func (c ClientTLS) Options(base Options) Options {
	base.CAFile, base.CertFile, base.KeyFile = c.CAFile, c.CertFile, c.KeyFile
	return base
}

// Key is a stable cache key of the identity: a digest of the three paths and of what the three files hold, so a certificate or an
// authority rotated on disk changes it and builds a new transport. The content is hashed, not just the size and the modification time:
// files unpacked from a reproducible archive, or written on a coarse file system, can be different and still have the same size and the
// same time (model p5-certloader). The empty identity has the empty key.
func (c ClientTLS) Key() string {
	if c.IsZero() {
		return ""
	}
	h := sha256.New()
	for _, p := range []string{c.CAFile, c.CertFile, c.KeyFile} {
		p = strings.TrimSpace(p)
		h.Write([]byte(p))
		h.Write([]byte{'|'})
		if p != "" {
			if body, err := os.ReadFile(p); err == nil {
				sum := sha256.Sum256(body)
				h.Write(sum[:])
			} else {
				h.Write([]byte("absent"))
			}
		}
		h.Write([]byte{';'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// certLoader serves a client certificate from a pair of files and reloads it when either file changes, so a rotated certificate is used by
// the next handshake without a restart. The two files are read at each handshake (they are a few kilobytes, and a handshake is a new
// connection) and the pair is parsed again only when their bytes differ from the ones cached: a stamp of size and modification time cannot
// tell two certificates apart when an archive normalises the times or the file system's clock is coarse (model p5-certloader). A pair that
// cannot be read or does not match fails that handshake only: nothing is cached for a failure, and the next handshake tries again.
type certLoader struct {
	certFile, keyFile string

	mu       sync.Mutex
	cert     *tls.Certificate
	certBody []byte
	keyBody  []byte
}

func (l *certLoader) get(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	certBody, err := os.ReadFile(l.certFile)
	if err != nil {
		return nil, fmt.Errorf("client certificate: %w", err)
	}
	keyBody, err := os.ReadFile(l.keyFile)
	if err != nil {
		return nil, fmt.Errorf("client key: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cert != nil && bytes.Equal(certBody, l.certBody) && bytes.Equal(keyBody, l.keyBody) {
		return l.cert, nil
	}
	pair, err := tls.X509KeyPair(certBody, keyBody)
	if err != nil {
		return nil, fmt.Errorf("client certificate: %w", err)
	}
	l.cert, l.certBody, l.keyBody = &pair, certBody, keyBody
	return l.cert, nil
}
