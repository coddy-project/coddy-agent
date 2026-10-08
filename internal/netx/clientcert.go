package netx

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
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

// Key is a stable cache key of the identity: a digest of the three paths with
// the size and modification time of each file, so a certificate rotated on disk
// changes it and builds a new transport. The empty identity has the empty key.
func (c ClientTLS) Key() string {
	if c.IsZero() {
		return ""
	}
	var b strings.Builder
	for _, p := range []string{c.CAFile, c.CertFile, c.KeyFile} {
		p = strings.TrimSpace(p)
		b.WriteString(p)
		b.WriteByte('|')
		if p != "" {
			if st, err := os.Stat(p); err == nil {
				b.WriteString(strconv.FormatInt(st.Size(), 10) + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10))
			} else {
				b.WriteString("absent")
			}
		}
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

// certLoader serves a client certificate from a pair of files and reloads it
// when either file changes, so a rotated certificate is used by the next
// handshake without a restart. A pair that cannot be read fails that handshake
// only: nothing is cached for a failure.
type certLoader struct {
	certFile, keyFile string

	mu       sync.Mutex
	cert     *tls.Certificate
	certSize int64
	certMod  time.Time
	keySize  int64
	keyMod   time.Time
}

func (l *certLoader) get(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	cs, err := os.Stat(l.certFile)
	if err != nil {
		return nil, fmt.Errorf("client certificate: %w", err)
	}
	ks, err := os.Stat(l.keyFile)
	if err != nil {
		return nil, fmt.Errorf("client key: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cert != nil && cs.Size() == l.certSize && cs.ModTime().Equal(l.certMod) && ks.Size() == l.keySize && ks.ModTime().Equal(l.keyMod) {
		return l.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(l.certFile, l.keyFile)
	if err != nil {
		return nil, fmt.Errorf("client certificate: %w", err)
	}
	l.cert, l.certSize, l.certMod, l.keySize, l.keyMod = &pair, cs.Size(), cs.ModTime(), ks.Size(), ks.ModTime()
	return l.cert, nil
}
