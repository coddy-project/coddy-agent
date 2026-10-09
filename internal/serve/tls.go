package serve

import (
	"fmt"
	"io"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

// EnsureCertificates makes the built-in TLS certificates of an agent home (coddy tls, internal/pki): what installing the service is for
// where TLS is concerned. It runs whatever the configuration says, so that a later `auto: true` finds them; the names of the server
// certificate come from the host, its interfaces and the addresses of the configuration, which a file that cannot be read does not
// stop. It prints what it did, and nothing when the files were right.
func EnsureCertificates(w io.Writer, home string) error {
	cli := config.CLIPaths{Home: home}
	var want pki.Want
	if cfg, err := config.LoadFromCLI(cli); err == nil {
		want = cfg.TLSWant()
	} else {
		host := pki.Hostname()
		want = pki.Want{Host: host, Names: pki.DefaultNames(host, nil, nil, nil)}
	}
	res, err := pki.Ensure(pki.Dir(home), want, nil)
	if err != nil {
		return fmt.Errorf("TLS certificates in %s: %w", pki.Dir(home), err)
	}
	if !res.Changed() {
		return nil
	}
	steps := make([]string, 0, len(res.Steps))
	for _, s := range res.Steps {
		steps = append(steps, string(s.Action))
	}
	_, _ = fmt.Fprintf(w, "TLS certificates in %s: %s\n", pki.Dir(home), strings.Join(steps, ", "))
	return nil
}
