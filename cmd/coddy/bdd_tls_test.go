package main

// Godog harness for features/tls_builtin.feature: `coddy tls` run in process against temporary homes, and a real mutual-TLS handshake
// between two of them through the same helpers the listener and the clients use.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

type tlsBDD struct {
	root   string
	homes  map[string]string
	out    bytes.Buffer
	server *httptest.Server
	issued [3]string // certificate, key, CA of the last `issue client`
	keys   map[string]string
}

func (s *tlsBDD) home(name string) string {
	if s.homes[name] == "" {
		s.homes[name] = filepath.Join(s.root, name)
	}
	return s.homes[name]
}

func (s *tlsBDD) tlsDir(name string) string { return pki.Dir(s.home(name)) }

func (s *tlsBDD) run(args ...string) error {
	s.out.Reset()
	return runTLS(args, &s.out, os.Stderr, strings.NewReader(""))
}

func (s *tlsBDD) noCertificates(name string) error {
	s.home(name)
	return nil
}

func (s *tlsBDD) withCertificates(name string) error {
	if err := s.run("ensure", "--quiet", "--home", s.home(name), "--name", "localhost"); err != nil {
		return err
	}
	return nil
}

func (s *tlsBDD) operatorRunsOn(command, name string) error {
	fields := strings.Fields(command)
	if len(fields) < 3 || fields[0] != "coddy" || fields[1] != "tls" {
		return fmt.Errorf("not a coddy tls command: %q", command)
	}
	return s.run(append(fields[2:], "--home", s.home(name))...)
}

func (s *tlsBDD) configAsks(name, key string) error {
	parts := strings.Split(key, ".")
	yaml := ""
	for i, p := range parts[:len(parts)-1] {
		yaml += strings.Repeat("  ", i) + p + ":\n"
	}
	yaml += strings.Repeat("  ", len(parts)-1) + parts[len(parts)-1] + ": true\n"
	return os.WriteFile(filepath.Join(s.homeDir(name), "config.yaml"), []byte(yaml), 0o600)
}

func (s *tlsBDD) configNamesFiles(name string) error {
	return os.WriteFile(filepath.Join(s.homeDir(name), "config.yaml"),
		[]byte("httpserver:\n  tls:\n    cert_file: /etc/coddy/s.crt\n    key_file: /etc/coddy/s.key\n"), 0o600)
}

// homeDir makes the home of a machine, so a configuration can be put in it.
func (s *tlsBDD) homeDir(name string) string {
	h := s.home(name)
	_ = os.MkdirAll(h, 0o700)
	return h
}

func (s *tlsBDD) runStarts(name string) error {
	_, err := loadRunConfig(config.CLIPaths{Home: s.home(name), CWD: s.root})
	return err
}

func (s *tlsBDD) hasNoDirectory(name string) error {
	if _, err := os.Stat(s.tlsDir(name)); err == nil {
		return fmt.Errorf("machine %s has a certificate directory it never asked for", name)
	}
	return nil
}

func (s *tlsBDD) hasEverything(name string) error {
	dir := s.tlsDir(name)
	for _, f := range []string{pki.CAFile, pki.CAKeyFile, pki.ServerCertFile, pki.ServerKeyFile, pki.ClientCertFile, pki.ClientKeyFile, pki.BundleFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return fmt.Errorf("machine %s lacks %s", name, f)
		}
	}
	return nil
}

func (s *tlsBDD) keysArePrivate(name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, f := range []string{pki.CAKeyFile, pki.ServerKeyFile, pki.ClientKeyFile} {
		info, err := os.Stat(filepath.Join(s.tlsDir(name), f))
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s has mode %v", f, info.Mode().Perm())
		}
	}
	return nil
}

func (s *tlsBDD) nothingToDo() error {
	if !strings.Contains(s.out.String(), "nothing to do") {
		return fmt.Errorf("output: %q", s.out.String())
	}
	return nil
}

func (s *tlsBDD) serves(name string) error {
	if s.server != nil {
		s.server.Close()
	}
	p := pki.PathsIn(s.tlsDir(name))
	cfg, err := netx.ClientCertTLS(p.Bundle, "test.client_ca_file")
	if err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(p.ServerCert, p.ServerKey)
	if err != nil {
		return err
	}
	cfg.Certificates = []tls.Certificate{pair}
	s.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	s.server.TLS = cfg
	s.server.Config.ErrorLog = nil
	s.server.StartTLS()
	return nil
}

func (s *tlsBDD) call(name string) error {
	p := pki.PathsIn(s.tlsDir(name))
	cfg, err := (netx.Options{CAFile: p.Bundle, CertFile: p.ClientCert, KeyFile: p.ClientKey}).TLSConfig("127.0.0.1")
	if err != nil {
		return err
	}
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
	resp, err := c.Get(s.server.URL)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (s *tlsBDD) cannotCall(name string) error {
	if err := s.call(name); err == nil {
		return fmt.Errorf("machine %s reached the server without being trusted", name)
	}
	return nil
}

func (s *tlsBDD) canCall(name string) error { return s.call(name) }

// exchange is what an operator does by hand: export on one machine, trust on the other.
func (s *tlsBDD) trustsTheCAOf(name, peer string) error {
	if err := s.run("export", "--home", s.home(peer)); err != nil {
		return err
	}
	file := filepath.Join(s.root, peer+"-ca.crt")
	if err := os.WriteFile(file, s.out.Bytes(), 0o644); err != nil {
		return err
	}
	return s.run("trust", file, "--home", s.home(name))
}

func (s *tlsBDD) noPrivateKeyMoved() error {
	for name, home := range s.homes {
		for _, f := range []string{pki.CAKeyFile, pki.ServerKeyFile, pki.ClientKeyFile} {
			key, err := os.ReadFile(filepath.Join(pki.Dir(home), f))
			if err != nil {
				return err
			}
			for other, ohome := range s.homes {
				if other == name {
					continue
				}
				entries, _ := os.ReadDir(filepath.Join(pki.Dir(ohome), pki.TrustedDir))
				for _, e := range entries {
					raw, _ := os.ReadFile(filepath.Join(pki.Dir(ohome), pki.TrustedDir, e.Name()))
					if bytes.Contains(raw, []byte("PRIVATE KEY")) || bytes.Equal(raw, key) {
						return fmt.Errorf("a private key of %s is in the trusted files of %s", name, other)
					}
				}
			}
		}
	}
	return nil
}

func (s *tlsBDD) issues(name, on string) error {
	out := filepath.Join(s.root, "issued")
	if err := s.run("issue", "client", name, "-o", out, "--home", s.home(on)); err != nil {
		return err
	}
	s.issued = [3]string{filepath.Join(out, name+".crt"), filepath.Join(out, name+".key"), filepath.Join(out, "ca.crt")}
	return nil
}

func (s *tlsBDD) issuedVerifies() error {
	read := func(path string) (*x509.Certificate, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		b, _ := pem.Decode(raw)
		if b == nil {
			return nil, fmt.Errorf("%s holds no certificate", path)
		}
		return x509.ParseCertificate(b.Bytes)
	}
	cert, err := read(s.issued[0])
	if err != nil {
		return err
	}
	ca, err := read(s.issued[2])
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.issued[1]); err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	_, err = cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}

func initializeTLSScenario(sc *godog.ScenarioContext) {
	s := &tlsBDD{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.root = os.TempDir()
		dir, err := os.MkdirTemp("", "coddy-tls-bdd-")
		if err != nil {
			return ctx, err
		}
		s.root, s.homes, s.server = dir, map[string]string{}, nil
		s.out.Reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.server != nil {
			s.server.Close()
		}
		_ = os.RemoveAll(s.root)
		return ctx, nil
	})
	sc.Step(`^a machine "([^"]*)" with no certificates$`, s.noCertificates)
	sc.Step(`^a machine "([^"]*)" with certificates$`, s.withCertificates)
	sc.Step(`^the operator runs "([^"]*)" on "([^"]*)"$`, s.operatorRunsOn)
	sc.Step(`^machine "([^"]*)" has a CA, a server pair, a client pair and a bundle$`, s.hasEverything)
	sc.Step(`^a machine "([^"]*)" whose configuration asks for the built-in certificates with "([^"]*)"$`, s.configAsks)
	sc.Step(`^a machine "([^"]*)" whose configuration names its own certificate files$`, s.configNamesFiles)
	sc.Step(`^a run starts on "([^"]*)"$`, s.runStarts)
	sc.Step(`^machine "([^"]*)" has no certificate directory$`, s.hasNoDirectory)
	sc.Step(`^the private keys of machine "([^"]*)" are readable by its owner only$`, s.keysArePrivate)
	sc.Step(`^the output says there was nothing to do$`, s.nothingToDo)
	sc.Step(`^machine "([^"]*)" serves HTTPS and asks for client certificates$`, s.serves)
	sc.Step(`^machine "([^"]*)" cannot call it$`, s.cannotCall)
	sc.Step(`^machine "([^"]*)" can call it$`, s.canCall)
	sc.Step(`^machine "([^"]*)" trusts the CA of machine "([^"]*)"$`, s.trustsTheCAOf)
	sc.Step(`^no private key of either machine was exchanged$`, s.noPrivateKeyMoved)
	sc.Step(`^the operator issues a client certificate named "([^"]*)" on "([^"]*)"$`, s.issues)
	sc.Step(`^the issued certificate verifies against the CA that was written next to it$`, s.issuedVerifies)
}

func TestTLSBuiltinFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "tls-builtin",
		ScenarioInitializer: initializeTLSScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/tls_builtin.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("tls builtin feature suite failed")
	}
}
