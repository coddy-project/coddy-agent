package pki

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// The files of the directory $CODDY_HOME/tls.
const (
	CAFile         = "ca.crt"
	CAKeyFile      = "ca.key"
	ServerCertFile = "server.crt"
	ServerKeyFile  = "server.key"
	ClientCertFile = "client.crt"
	ClientKeyFile  = "client.key"
	BundleFile     = "bundle.pem"
	TrustedDir     = "trusted"
	lockName       = ".lock"
)

// DirName is the directory under the coddy home.
const DirName = "tls"

// Dir is the built-in PKI directory of a coddy home.
func Dir(home string) string { return filepath.Join(home, DirName) }

// Paths are the files a configuration points at in the built-in mode.
type Paths struct {
	Dir                       string
	CA                        string // the CA certificate of this machine, what `coddy tls export` prints
	Bundle                    string // this CA and every trusted one: ca_file and client_ca_file
	ServerCert, ServerKey     string
	ClientCert, ClientKey     string
	TrustedDirectory, LockDir string
}

// PathsIn names the files under dir.
func PathsIn(dir string) Paths {
	return Paths{
		Dir: dir, CA: filepath.Join(dir, CAFile), Bundle: filepath.Join(dir, BundleFile),
		ServerCert: filepath.Join(dir, ServerCertFile), ServerKey: filepath.Join(dir, ServerKeyFile),
		ClientCert: filepath.Join(dir, ClientCertFile), ClientKey: filepath.Join(dir, ClientKeyFile),
		TrustedDirectory: filepath.Join(dir, TrustedDir), LockDir: dir,
	}
}

// Load reads the directory into a State. Nothing in it is trusted: a file that does not parse, or a key that does not match, is a nil
// field or a false flag, which the planner turns into a step. A missing directory is an empty State.
func Load(dir string) State {
	var st State
	if cert, key := readCertAndKey(filepath.Join(dir, CAFile), filepath.Join(dir, CAKeyFile)); cert != nil {
		st.CA = cert
		st.CAKeyOK = key != nil && keyMatches(cert, key)
	}
	st.Server = readLeaf(filepath.Join(dir, ServerCertFile), filepath.Join(dir, ServerKeyFile))
	st.Client = readLeaf(filepath.Join(dir, ClientCertFile), filepath.Join(dir, ClientKeyFile))
	st.Trusted = readTrusted(filepath.Join(dir, TrustedDir))
	if raw, err := os.ReadFile(filepath.Join(dir, BundleFile)); err == nil { // #nosec G304 -- a file of the coddy home
		st.Bundle = raw
	}
	return st
}

func readLeaf(certPath, keyPath string) *Leaf {
	cert, key := readCertAndKey(certPath, keyPath)
	if cert == nil {
		return nil
	}
	return &Leaf{Cert: cert, KeyMatches: key != nil && keyMatches(cert, key)}
}

func readCertAndKey(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey) {
	cert := readCert(certPath)
	if cert == nil {
		return nil, nil
	}
	return cert, readKey(keyPath)
}

func readCert(path string) *x509.Certificate {
	raw, err := os.ReadFile(path) // #nosec G304 -- a file of the coddy home
	if err != nil {
		return nil
	}
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return nil
		}
		if block.Type == "CERTIFICATE" {
			if c, err := x509.ParseCertificate(block.Bytes); err == nil {
				return c
			}
			return nil
		}
	}
}

func readKey(path string) *ecdsa.PrivateKey {
	raw, err := os.ReadFile(path) // #nosec G304 -- a file of the coddy home
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ec, ok := k.(*ecdsa.PrivateKey); ok {
			return ec
		}
	}
	return nil
}

func keyMatches(c *x509.Certificate, k *ecdsa.PrivateKey) bool {
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	return ok && pub.Equal(&k.PublicKey)
}

func readTrusted(dir string) []*x509.Certificate {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".crt") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []*x509.Certificate
	for _, n := range names {
		if c := readCert(filepath.Join(dir, n)); c != nil && c.IsCA {
			out = append(out, c)
		}
	}
	return out
}

// Result is what Ensure did.
type Result struct {
	// Steps are the steps applied, in order, with their reasons. Empty means the state was already right.
	Steps []Step
	// NewCA is true when the CA was made or replaced: the peers that trust the old one must trust this one (`coddy tls export`).
	NewCA bool
}

// Changed reports whether anything was written.
func (r Result) Changed() bool { return len(r.Steps) > 0 }

// maxPasses bounds the loop of Ensure: a CA first, then the leaves and the bundle, then a check that the plan is empty.
const maxPasses = 4

// Ensure makes the directory right for want: it plans, applies, and plans again until the plan is empty. It runs under an exclusive
// file lock, so the installer, an update, a service start and an operator running it at once do not interleave; the others wait and
// then find nothing to do. Every file is written to a temporary name and renamed, a key before its certificate, so a crash leaves a state
// the next run repairs and never a certificate without its key.
func Ensure(dir string, w Want, now func() time.Time) (Result, error) {
	return ensure(dir, w, now, osFS{})
}

func ensure(dir string, w Want, now func() time.Time, fs writer) (Result, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create %s: %w", dir, err)
	}
	unlock, err := platform.LockFile(filepath.Join(dir, lockName))
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	return applyLocked(dir, w, now, fs)
}

// applyLocked is Ensure with the lock held by the caller.
func applyLocked(dir string, w Want, now func() time.Time, fs writer) (Result, error) {
	var res Result
	host := w.Host
	if host == "" {
		host = "localhost"
	}
	// A forced renewal acts once: the second pass sees a new CA and must not force another.
	forceCA, renew := w.ForceCA, w.RenewLeaves
	for pass := 0; pass < maxPasses; pass++ {
		w.ForceCA, w.RenewLeaves = forceCA, renew
		st := Load(dir)
		steps := Plan(st, w, now())
		if len(steps) == 0 {
			return res, nil
		}
		forceCA, renew = false, false
		var caCert *x509.Certificate
		var caKey *ecdsa.PrivateKey
		for _, step := range steps {
			switch step.Action {
			case CreateCA, RenewCA:
				c, k, err := newCA(host, now(), w.caValidity())
				if err != nil {
					return res, err
				}
				if err := writePair(fs, filepath.Join(dir, CAFile), filepath.Join(dir, CAKeyFile), CertPEM(c), k); err != nil {
					return res, err
				}
				caCert, caKey = c, k
				res.NewCA = true
			case IssueServer, IssueClient:
				if caCert == nil {
					caCert, caKey = readCertAndKey(filepath.Join(dir, CAFile), filepath.Join(dir, CAKeyFile))
					if caCert == nil || caKey == nil {
						return res, errors.New("the CA is unusable: run `coddy tls ensure --force-ca`")
					}
				}
				server := step.Action == IssueServer
				c, k, err := newLeaf(caCert, caKey, host, w.Names, server, now(), w.leafValidity())
				if err != nil {
					return res, err
				}
				certPath, keyPath := filepath.Join(dir, ClientCertFile), filepath.Join(dir, ClientKeyFile)
				if server {
					certPath, keyPath = filepath.Join(dir, ServerCertFile), filepath.Join(dir, ServerKeyFile)
				}
				if err := writePair(fs, certPath, keyPath, CertPEM(c), k); err != nil {
					return res, err
				}
			case WriteBundle:
				ca := caCert
				if ca == nil {
					ca = st.CA
				}
				if err := fs.writeFile(filepath.Join(dir, BundleFile), BundlePEM(ca, st.Trusted), 0o644); err != nil {
					return res, err
				}
			}
			res.Steps = append(res.Steps, step)
		}
	}
	return res, fmt.Errorf("the plan for %s is not empty after %d passes: this is a bug", dir, maxPasses)
}

// writePair writes a key and then its certificate, each atomically: the key first, so a certificate never exists without its key.
func writePair(fs writer, certPath, keyPath string, certPEM []byte, key *ecdsa.PrivateKey) error {
	keyPEM, err := KeyPEM(key)
	if err != nil {
		return err
	}
	if err := fs.writeFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	return fs.writeFile(certPath, certPEM, 0o644)
}

// writer is the one place a file is written, so a test can make a write fail at any step.
type writer interface {
	writeFile(path string, data []byte, perm os.FileMode) error
}

type osFS struct{}

// writeFile writes data to a temporary file in the directory of path and renames it over path.
func (osFS) writeFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(name, perm); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ErrNotCA is returned by Trust for a certificate that is not a CA.
var ErrNotCA = errors.New("that certificate is not a CA (CA:TRUE is missing): a peer's CA is what to trust, never a leaf")

// Trust adds the CA certificate in pemData to trusted/ (once, by its fingerprint) and rebuilds the bundle. It refuses what is not a CA
// and what has expired, and it never accepts a private key. It reports whether anything was added.
func Trust(dir string, pemData []byte, now time.Time) (added bool, fingerprint string, err error) {
	var cert *x509.Certificate
	rest := pemData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return false, "", errors.New("that is a private key: trust takes a CA certificate, and a private key must never be passed around")
		}
		if block.Type == "CERTIFICATE" && cert == nil {
			if c, perr := x509.ParseCertificate(block.Bytes); perr == nil {
				cert = c
			}
		}
	}
	if cert == nil {
		return false, "", errors.New("no PEM certificate found")
	}
	if !cert.IsCA || !cert.BasicConstraintsValid {
		return false, "", ErrNotCA
	}
	if !now.Before(cert.NotAfter) {
		return false, "", fmt.Errorf("that CA expired on %s", cert.NotAfter.UTC().Format("2006-01-02"))
	}
	fingerprint = Fingerprint(cert)
	if err := os.MkdirAll(filepath.Join(dir, TrustedDir), 0o700); err != nil {
		return false, "", err
	}
	unlock, err := platform.LockFile(filepath.Join(dir, lockName))
	if err != nil {
		return false, "", err
	}
	defer unlock()
	path := filepath.Join(dir, TrustedDir, fingerprint[:16]+".crt")
	if _, serr := os.Stat(path); serr == nil {
		return false, fingerprint, nil
	}
	fs := osFS{}
	if err := fs.writeFile(path, CertPEM(cert), 0o644); err != nil {
		return false, "", err
	}
	st := Load(dir)
	if err := fs.writeFile(filepath.Join(dir, BundleFile), BundlePEM(st.CA, st.Trusted), 0o644); err != nil {
		return false, "", err
	}
	return true, fingerprint, nil
}

// Fingerprint is the SHA-256 of the certificate, in hex.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

var clientNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// IssueClientFiles makes a client certificate for a machine or a tool that has no Coddy (a script, a proxy), signed by this machine's CA,
// and writes name.crt and name.key into outDir (key 0600) together with ca.crt, the CA to trust. The CA key never leaves the directory.
func IssueClientFiles(dir, name, outDir string, now time.Time, validity time.Duration) (certPath, keyPath, caPath string, err error) {
	if !clientNameRE.MatchString(name) {
		return "", "", "", fmt.Errorf("%q is not a usable name: letters, digits, '.', '_' and '-', up to 63 characters", name)
	}
	if validity <= 0 {
		validity = DefaultLeafValidity
	}
	unlock, err := platform.LockFile(filepath.Join(dir, lockName))
	if err != nil {
		return "", "", "", err
	}
	defer unlock()
	ca, caKey := readCertAndKey(filepath.Join(dir, CAFile), filepath.Join(dir, CAKeyFile))
	if ca == nil || caKey == nil || !keyMatches(ca, caKey) {
		return "", "", "", errors.New("there is no CA: run `coddy tls ensure` first")
	}
	c, k, err := newLeaf(ca, caKey, name, nil, false, now, validity)
	if err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", "", "", err
	}
	fs := osFS{}
	certPath, keyPath, caPath = filepath.Join(outDir, name+".crt"), filepath.Join(outDir, name+".key"), filepath.Join(outDir, "ca.crt")
	if err := writePair(fs, certPath, keyPath, CertPEM(c), k); err != nil {
		return "", "", "", err
	}
	if err := fs.writeFile(caPath, CertPEM(ca), 0o644); err != nil {
		return "", "", "", err
	}
	return certPath, keyPath, caPath, nil
}

// ExportCA is the PEM of this machine's CA certificate: the one thing to hand to a peer so that it trusts this machine.
func ExportCA(dir string) ([]byte, error) {
	ca := readCert(filepath.Join(dir, CAFile))
	if ca == nil {
		return nil, errors.New("there is no CA: run `coddy tls ensure` first")
	}
	return CertPEM(ca), nil
}
