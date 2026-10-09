package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

func tlsRun(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out, errw bytes.Buffer
	err := runTLS(args, &out, &errw, strings.NewReader(stdin))
	return out.String(), err
}

func TestTLSUsageErrors(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no verb", nil, "usage:"},
		{"unknown verb", []string{"frobnicate", "--home", home}, "unknown tls subcommand"},
		{"trust without a file", []string{"trust", "--home", home}, "tls trust"},
		{"issue without a kind", []string{"issue", "x", "--home", home}, "tls issue client"},
		{"issue a server", []string{"issue", "server", "x", "--home", home}, "tls issue client"},
		{"export with no CA", []string{"export", "--home", home}, "tls ensure"},
		{"unknown flag", []string{"ensure", "--nope", "--home", home}, "flag provided but not defined"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := tlsRun(t, "", c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one naming %q", err, c.want)
			}
		})
	}
}

func TestTLSEnsureQuietPrintsNothingAndStatusSaysWhatIsThere(t *testing.T) {
	home := t.TempDir()
	if out, err := tlsRun(t, "", "ensure", "--quiet", "--home", home, "--name", "relay.example", "--name", "10.9.8.7"); err != nil || out != "" {
		t.Fatalf("quiet ensure: out=%q err=%v", out, err)
	}
	out, err := tlsRun(t, "", "status", "--json", "--home", home, "--name", "relay.example", "--name", "10.9.8.7")
	if err != nil {
		t.Fatal(err)
	}
	var s pki.Status
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("status --json is not JSON: %v\n%s", err, out)
	}
	if !s.CA.Present || !s.Server.Present || !s.Client.Present || !s.BundleOK || len(s.Pending) != 0 {
		t.Fatalf("status = %+v", s)
	}
	joined := strings.Join(s.Server.Names, " ")
	for _, want := range []string{"relay.example", "10.9.8.7", "localhost", "127.0.0.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the server certificate lacks %s: %v", want, s.Server.Names)
		}
	}
	text, err := tlsRun(t, "", "status", "--home", home)
	if err != nil || !strings.Contains(text, "bundle.pem is current") || strings.Contains(text, "warning:") {
		t.Fatalf("human status: %q %v", text, err)
	}
	// A new name is a reason to issue the server certificate again, and status says so before ensure does it.
	text, _ = tlsRun(t, "", "status", "--home", home, "--name", "new.example")
	if !strings.Contains(text, "pending: issue-server") || !strings.Contains(text, "new.example") {
		t.Fatalf("status does not announce the new name: %q", text)
	}
}

func TestTLSRenewIssuesTheLeavesAgainAndKeepsTheCA(t *testing.T) {
	home := t.TempDir()
	if _, err := tlsRun(t, "", "ensure", "--quiet", "--home", home); err != nil {
		t.Fatal(err)
	}
	ca, _ := os.ReadFile(filepath.Join(pki.Dir(home), pki.CAFile))
	srv, _ := os.ReadFile(filepath.Join(pki.Dir(home), pki.ServerCertFile))
	out, err := tlsRun(t, "", "renew", "--home", home)
	if err != nil || !strings.Contains(out, "issue-server") || !strings.Contains(out, "issue-client") || strings.Contains(out, "create-ca") {
		t.Fatalf("renew: %q %v", out, err)
	}
	if now, _ := os.ReadFile(filepath.Join(pki.Dir(home), pki.CAFile)); string(now) != string(ca) {
		t.Fatal("renew replaced the CA")
	}
	if now, _ := os.ReadFile(filepath.Join(pki.Dir(home), pki.ServerCertFile)); string(now) == string(srv) {
		t.Fatal("renew left the server certificate as it was")
	}
}

func TestTLSForceCAWarnsThatPeersMustTrustTheNewOne(t *testing.T) {
	home := t.TempDir()
	_, _ = tlsRun(t, "", "ensure", "--quiet", "--home", home)
	out, err := tlsRun(t, "", "ensure", "--force-ca", "--home", home)
	if err != nil || !strings.Contains(out, "renew-ca") || !strings.Contains(out, "every peer") {
		t.Fatalf("force-ca: %q %v", out, err)
	}
}

func TestTLSTrustReadsStdinRefusesKeysAndLeavesAndIsIdempotent(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	_, _ = tlsRun(t, "", "ensure", "--quiet", "--home", a)
	_, _ = tlsRun(t, "", "ensure", "--quiet", "--home", b)
	ca, err := tlsRun(t, "", "export", "--home", a)
	if err != nil || !strings.HasPrefix(ca, "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("export: %q %v", ca, err)
	}
	if strings.Contains(ca, "PRIVATE KEY") {
		t.Fatal("export printed a private key")
	}
	if out, err := tlsRun(t, ca, "trust", "-", "--home", b); err != nil || !strings.Contains(out, "trusted CA") {
		t.Fatalf("trust -: %q %v", out, err)
	}
	if out, err := tlsRun(t, ca, "trust", "-", "--home", b); err != nil || !strings.Contains(out, "already trusted") {
		t.Fatalf("second trust: %q %v", out, err)
	}
	key, _ := os.ReadFile(filepath.Join(pki.Dir(a), pki.CAKeyFile))
	if _, err := tlsRun(t, string(key), "trust", "-", "--home", b); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Fatalf("a private key was accepted: %v", err)
	}
	leaf := filepath.Join(pki.Dir(a), pki.ServerCertFile)
	if _, err := tlsRun(t, "", "trust", leaf, "--home", b); err == nil || !strings.Contains(err.Error(), "not a CA") {
		t.Fatalf("a leaf was accepted: %v", err)
	}
}

func TestTLSIssueClientPositionalsAndFlagsInAnyOrder(t *testing.T) {
	home, out := t.TempDir(), filepath.Join(t.TempDir(), "o")
	_, _ = tlsRun(t, "", "ensure", "--quiet", "--home", home)
	for i, args := range [][]string{
		{"issue", "client", "ci", "-o", out, "--home", home},
		{"issue", "client", "ci2", "--home", home, "-o", out},
	} {
		text, err := tlsRun(t, "", args...)
		if err != nil || !strings.Contains(text, "keep it private") {
			t.Fatalf("form %d: %q %v", i, text, err)
		}
	}
	for _, f := range []string{"ci.crt", "ci.key", "ci2.crt", "ci2.key", "ca.crt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// A configuration that cannot be read must not stop the certificates being made: they are needed to start.
func TestTLSEnsureSurvivesABrokenConfiguration(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("agent: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := runTLS([]string{"ensure", "--quiet", "--home", home}, &out, &errw, strings.NewReader("")); err != nil {
		t.Fatalf("ensure failed on a broken config: %v", err)
	}
	if !strings.Contains(errw.String(), "configuration was not read") {
		t.Errorf("the broken file was not mentioned: %q", errw.String())
	}
	if !pki.Describe(pki.Dir(home), pki.Want{Host: "h"}, time.Now()).CA.Present {
		t.Error("no CA was made")
	}
}

func TestTLSHostsComeFromTheBindAndAdvertiseAddresses(t *testing.T) {
	var cfg config.Config
	cfg.HTTPServer.Host = "0.0.0.0"
	cfg.Swarm.Host = "relay.internal"
	cfg.Swarm.Join = []config.SwarmJoin{{AdvertiseURL: "https://node7.example:12346"}, {}}
	want := tlsWant(&cfg, []string{"extra.example"})
	got := strings.Join(want.Names, " ")
	for _, n := range []string{"relay.internal", "node7.example", "extra.example", "localhost"} {
		if !strings.Contains(got, n) {
			t.Errorf("names lack %s: %v", n, want.Names)
		}
	}
	if strings.Contains(got, "0.0.0.0") {
		t.Errorf("a wildcard bind address is a name: %v", want.Names)
	}
	if hosts := (*config.Config)(nil).TLSHosts(); hosts != nil {
		t.Errorf("a nil config has hosts: %v", hosts)
	}
}

// `coddy tls ensure --if-used` is what an update runs: nothing for a machine that never used the certificates, a renewal for one that
// does, and only what changed is printed.
func TestTLSEnsureIfUsedActsOnlyWhereTheCertificatesAreInUse(t *testing.T) {
	home := t.TempDir()
	if out, err := tlsRun(t, "", "ensure", "--if-used", "--home", home); err != nil || out != "" {
		t.Fatalf("unused: %q %v", out, err)
	}
	if _, err := os.Stat(pki.Dir(home)); err == nil {
		t.Fatal("--if-used made a certificate directory on a machine that never asked for one")
	}

	// A configuration that asks for them is a use.
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("httpserver:\n  tls:\n    auto: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tlsRun(t, "", "ensure", "--if-used", "--home", home)
	if err != nil || !strings.Contains(out, "create-ca") {
		t.Fatalf("asked for by a block: %q %v", out, err)
	}
	if out, err := tlsRun(t, "", "ensure", "--if-used", "--home", home); err != nil || out != "" {
		t.Fatalf("nothing changed, nothing printed: %q %v", out, err)
	}

	// A directory that exists is a use too: an expired leaf is issued again.
	other := t.TempDir()
	_, _ = tlsRun(t, "", "ensure", "--quiet", "--home", other)
	_ = os.Remove(filepath.Join(pki.Dir(other), pki.ServerCertFile))
	if out, err := tlsRun(t, "", "ensure", "--if-used", "--home", other); err != nil || !strings.Contains(out, "issue-server") {
		t.Fatalf("a directory in use: %q %v", out, err)
	}
}
