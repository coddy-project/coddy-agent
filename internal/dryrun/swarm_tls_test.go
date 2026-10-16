package dryrun

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSwarmTLSFilesAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{name: "missing"},
		{name: "invalid PEM", content: "not a certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
			if tc.content != "" {
				for _, file := range []string{cert, key} {
					if err := os.WriteFile(file, []byte(tc.content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			prep, _ := prepare(t, fmt.Sprintf("swarm:\n  enable: true\n  host: 127.0.0.1\n  tls:\n    cert_file: %q\n    key_file: %q\n", cert, key))
			r := &runner{req: Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator}, rep: &Report{}}
			r.paths()
			check := find(t, r.rep, "swarm.tls")
			if check.Status != StatusError || check.Line == 0 || check.Fix == "" {
				t.Errorf("TLS check: %+v", check)
			}
		})
	}
}

func TestValidSwarmTLSFilesAreAccepted(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for file, data := range map[string][]byte{
		certFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}),
		keyFile:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prep, _ := prepare(t, fmt.Sprintf("swarm:\n  enable: true\n  host: 127.0.0.1\n  tls:\n    cert_file: %q\n    key_file: %q\n", certFile, keyFile))
	r := &runner{req: Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator}, rep: &Report{}}
	r.paths()
	check := find(t, r.rep, "swarm.tls")
	if check.Status != StatusOK || check.Line == 0 {
		t.Errorf("valid TLS files: %+v", check)
	}
}

func TestDisabledSwarmDoesNotCheckTLSFiles(t *testing.T) {
	prep, _ := prepare(t, "swarm:\n  enable: false\n  tls:\n    cert_file: missing.pem\n    key_file: missing-key.pem\n")
	r := &runner{req: Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator}, rep: &Report{}}
	r.paths()
	for _, check := range r.rep.Checks {
		if check.Path == "swarm.tls" {
			t.Errorf("disabled relay checked TLS files: %+v", check)
		}
	}
}
