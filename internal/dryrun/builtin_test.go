package dryrun

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

const builtinYAML = `providers:
  - name: remote
    type: coddy
    api_base: https://127.0.0.1:1
    api_key: shared-token
    proxy: none
    tls_auto: true
models:
  - model: remote/coder
agent:
  model: remote/coder
httpserver:
  enable: true
  tls:
    auto: true
`

func hasCheck(rep *Report, status Status, substr string) bool {
	for _, c := range rep.Checks {
		if c.Status == status && strings.Contains(c.Message, substr) {
			return true
		}
	}
	return false
}

// Certificates that the configuration asks for and that are not there yet are reported once and nothing reads them: `coddy serve`
// makes them at start, so a dry run does not call that an error and does not probe with files that are about to exist.
func TestBuiltinCertificatesNotThereYetAreOneWarningAndSkipTheirProbes(t *testing.T) {
	rep := run(t, builtinYAML, nil)
	if !hasCheck(rep, StatusWarning, "not all in") {
		t.Fatalf("no warning about the missing built-in certificates: %+v", rep.Checks)
	}
	for _, c := range rep.Checks {
		if c.Status == StatusError {
			t.Errorf("a dry run before the first start reported an error: %+v", c)
		}
	}
	if c := find(t, rep, "providers[remote]"); c.Status != StatusSkipped || !strings.Contains(c.Message, "built-in certificates are not made yet") {
		t.Errorf("the provider was probed with files that do not exist: %+v", c)
	}
}

// Once they are there the files are checked like any other, and a certificate that is ending is a warning.
func TestBuiltinCertificatesThatExistAreCheckedLikeNamedFiles(t *testing.T) {
	var home string
	rep := run(t, builtinYAML, func(r *Request) {
		home = r.Paths.Home
		if _, err := pki.Ensure(pki.Dir(home), r.Cfg.TLSWant(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if hasCheck(rep, StatusWarning, "not all in") {
		t.Fatalf("the certificates exist, yet the dry run says they do not: %+v", rep.Checks)
	}
	if c := find(t, rep, "httpserver.tls"); c.Status != StatusOK {
		t.Errorf("the built-in server pair was not checked: %+v", c)
	}

	// The same files, read 340 days later, are within their renewal window.
	late := run(t, builtinYAML, func(r *Request) {
		_, _ = pki.Ensure(pki.Dir(r.Paths.Home), r.Cfg.TLSWant(), func() time.Time { return time.Now().Add(-340 * 24 * time.Hour) })
	})
	if !hasCheck(late, StatusWarning, "coddy tls ensure") && !hasCheck(late, StatusWarning, "ends in") {
		t.Errorf("certificates near their end were not reported: %+v", late.Checks)
	}
}

func TestADryRunWithNoBuiltinBlockSaysNothingAboutThem(t *testing.T) {
	rep := run(t, "providers: []\nmodels: []\n", nil)
	if hasCheck(rep, StatusWarning, "built-in") {
		t.Fatalf("%+v", rep.Checks)
	}
}
