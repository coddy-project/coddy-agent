//go:build swarm

package swarm

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The principal of a request at the relay (docs/plans/remote-model-provider-phase3.md, 3.2 and 4.2; D2 closed by
// docs/plans/remote-model-provider-models/p3-d2-mtls-identity.md): who the gate lets through, resolved once per request.

type principalClass int

const (
	// principalNone is a request no credential of the relay vouches for.
	principalNone principalClass = iota
	// principalFull is a holder of swarm.auth_token (or a token given out of band): the whole relay, as before.
	principalFull
	// principalScoped is a swarm.clients entry: the three shared-model routes of the nodes the entry lists, and nothing else.
	principalScoped
)

type principal struct {
	class  principalClass
	client *config.SwarmClient // the entry, for principalScoped
}

type principalCtxKey struct{}

func withPrincipal(r *http.Request, p principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalCtxKey{}, p))
}

func principalFrom(ctx context.Context) principal {
	p, _ := ctx.Value(principalCtxKey{}).(principal)
	return p
}

// SetClients replaces the scoped entries, for a caller that holds the relay while its configuration moves. A settings save
// rebuilds the relay, so this is the live read the verdict of D2 asks for: removing an entry or a name takes effect on the next
// request, also on a connection that is already open.
func (s *Server) SetClients(clients []config.SwarmClient) {
	s.mu.Lock()
	s.clients = append([]config.SwarmClient(nil), clients...)
	s.mu.Unlock()
}

func (s *Server) scopedClients() []config.SwarmClient {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]config.SwarmClient(nil), s.clients...)
}

// allClientTokens is every token that proves a client of this relay, full or scoped: the ones a media capability must never be
// confused with and that are never handed to a node.
func (s *Server) allClientTokens() []string {
	out := s.clientTokens()
	for _, c := range s.scopedClients() {
		if c.Token != "" {
			out = append(out, c.Token)
		}
	}
	return out
}

// authRequired reports whether the relay asks for a credential at all: a full token, or a scoped entry anybody can authenticate as.
// A relay with neither is open, as it always was.
func (s *Server) authRequired() bool {
	if len(s.clientTokens()) > 0 {
		return true
	}
	for _, c := range s.scopedClients() {
		if c.HasCredential() {
			return true
		}
	}
	return false
}

// principalOf resolves the request against every entry, without an early exit.
//
// An entry with a token only is a bearer entry, with certificate names only a certificate entry, with both it needs BOTH, the
// bearer and a verified certificate that maps to that same entry (the token is useless without the key, and the key without the
// token). A token that is also a full token is scoped: least privilege, and load refuses the duplicate anyway. A certificate
// never opens the full class. The identity is read from r.TLS on this request against the live entries: nothing is cached in the
// connection, so a name removed from the configuration is refused on the next request of an open connection, and a resumed
// connection is judged like a fresh one.
func (s *Server) principalOf(r *http.Request) principal {
	bearer := credentialOf(r)
	names := certificateNames(r, time.Now())
	clients := s.scopedClients()
	var scoped *config.SwarmClient
	bearerIsScopedToken := false
	for i := range clients {
		c := &clients[i]
		tokenOK := c.Token != "" && bearer != "" && subtle.ConstantTimeCompare([]byte(c.Token), []byte(bearer)) == 1
		if tokenOK {
			bearerIsScopedToken = true
		}
		certOK := len(c.CertNames) > 0 && namesMatch(names, c.CertNames)
		var eligible bool
		switch {
		case c.Token != "" && len(c.CertNames) > 0:
			eligible = tokenOK && certOK
		case c.Token != "":
			eligible = tokenOK
		case len(c.CertNames) > 0:
			eligible = certOK
		}
		if eligible && scoped == nil {
			scoped = c
		}
	}
	// A bearer that is a full token proves the full class, whatever certificate rides with it: a certificate never lowers a
	// credential and never raises one. A bearer that is a token of a scoped entry too (load refuses the duplicate) is scoped, the
	// narrower class, and when that entry is not eligible (it binds a certificate the request lacks) it proves nothing.
	if !bearerIsScopedToken && acceptToken(s.clientTokens(), bearer) {
		return principal{class: principalFull}
	}
	if scoped != nil {
		return principal{class: principalScoped, client: scoped}
	}
	return principal{class: principalNone}
}

// certificateNames are the DNS and URI names of the verified client certificate of the request, or nil when there is none: no
// TLS, no certificate offered, or a leaf that is not valid now (checked per request, since a connection outlives a certificate).
// The CN is not consulted. The chain was verified against swarm.tls.client_ca_file at the handshake; a connection that offered
// none has no verified chain here.
func certificateNames(r *http.Request, now time.Time) []string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return nil
	}
	leaf := r.TLS.VerifiedChains[0][0]
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil
	}
	out := make([]string, 0, len(leaf.DNSNames)+len(leaf.URIs))
	out = append(out, leaf.DNSNames...)
	for _, u := range leaf.URIs {
		out = append(out, u.String())
	}
	return out
}

// namesMatch reports whether any certificate name is one of the entry's, exactly.
func namesMatch(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.TrimSpace(w) == h {
				return true
			}
		}
	}
	return false
}

// ClientCertTLS builds the server side of the relay's client certificates from swarm.tls: the pool of swarm.tls.client_ca_file
// and the mode, optional (verify a certificate when one is offered, let a peer without one in for the routes that need none) or
// required (refuse a peer without a verified certificate at the handshake, nodes that join and browsers included). It returns nil
// when no client CA is set: the listener then asks for no certificate. The server certificate is the caller's (ListenAndServeTLS
// loads cert_file and key_file). The CA is startup state: changing it takes a restart.
func ClientCertTLS(t config.SwarmTLSConfig) (*tls.Config, error) {
	mode := t.EffectiveClientAuth()
	if mode == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(t.ClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("swarm.tls.client_ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("swarm.tls.client_ca_file %q holds no PEM certificate", t.ClientCAFile)
	}
	auth := tls.VerifyClientCertIfGiven
	if mode == config.SwarmClientAuthRequired {
		auth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ClientCAs: pool, ClientAuth: auth}, nil
}
