package dryrun

import (
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

// builtinTLS reports on the built-in certificates (coddy tls, `auto: true`) when a block of the configuration asks for them. Nothing
// is written: a start of `coddy serve` makes the missing ones. It returns true while some are missing, so the file checks and the
// probes that would read them are skipped instead of failing on files that are about to exist.
func (r *runner) builtinTLS() bool {
	cfg := r.req.Cfg
	if !cfg.BuiltinTLSWanted() {
		return false
	}
	dir := pki.Dir(r.req.Paths.Home)
	s := pki.Describe(dir, cfg.TLSWant(), time.Now())
	key := builtinKey(r)
	if !s.CA.Present || !s.Server.Present || !s.Client.Present {
		r.rep.add(r.check(StatusWarning, key, key, "the built-in TLS certificates are not all in "+dir+" yet",
			"`coddy serve` makes them at start; `coddy tls ensure` makes them now"))
		return true
	}
	for _, f := range s.Findings(cfg.TLSWant().RenewBefore) {
		r.rep.add(r.check(StatusWarning, key, key, f, "`coddy tls ensure` makes what is missing or ending"))
	}
	return false
}

// builtinKey is the key a finding about the built-in certificates is located at: the first block that asks for them.
func builtinKey(r *runner) string {
	cfg := r.req.Cfg
	switch {
	case cfg.HTTPServer.TLS.Auto:
		return "httpserver.tls.auto"
	case cfg.Swarm.TLS.Auto:
		return "swarm.tls.auto"
	case cfg.Swarm.NodeTLS.Auto:
		return "swarm.node_tls.auto"
	}
	return "tls"
}
