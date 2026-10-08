package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func selfSigned(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kb, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile = filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile
}

func identityCheckYAML(ca, cert, key string) string {
	return withModeline(fmt.Sprintf("providers:\n  - name: remote\n    type: coddy\n    api_base: https://remote.example:12345\n    ca_file: %s\n    client_cert_file: %s\n    client_key_file: %s\n", ca, cert, key))
}

func TestCheckReadsTheProviderIdentityFiles(t *testing.T) {
	noOutOfBandCredentials(t)
	dir := t.TempDir()
	certA, keyA := selfSigned(t, dir, "a")
	_, keyB := selfSigned(t, dir, "b")

	rep := checkYAML(t, identityCheckYAML(certA, certA, keyA))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("a readable pair and a CA bundle must pass silently: %+v", rep.Findings)
	}

	// A mismatched pair is an error at the certificate key.
	rep = checkYAML(t, identityCheckYAML(certA, certA, keyB))
	f := findingAt(t, rep.Findings, SeverityError, "providers[remote].client_cert_file")
	if f.Line == 0 || f.Fix == "" || !strings.Contains(f.Message, "key") {
		t.Fatalf("mismatched pair finding: %+v", f)
	}

	// An unreadable pair is an error.
	absent := filepath.Join(dir, "absent.pem")
	rep = checkYAML(t, identityCheckYAML(certA, absent, absent))
	findingAt(t, rep.Findings, SeverityError, "providers[remote].client_cert_file")

	// A CA file that holds no certificate is a warning, a missing one an error.
	empty := filepath.Join(dir, "empty.pem")
	_ = os.WriteFile(empty, []byte("not pem"), 0o600)
	rep = checkYAML(t, identityCheckYAML(empty, certA, keyA))
	findingAt(t, rep.Findings, SeverityWarning, "providers[remote].ca_file")
	rep = checkYAML(t, identityCheckYAML(absent, certA, keyA))
	findingAt(t, rep.Findings, SeverityError, "providers[remote].ca_file")
}
