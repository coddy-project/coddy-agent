//go:build swarm

package swarm

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The principal of a request at the relay, resolved once per request.
// Scoped principals are bearer-token entries.

type principalClass int

const (
	// principalNone is a request no credential of the relay vouches for.
	principalNone principalClass = iota
	// principalFull is a holder of swarm.auth_token (or a token given out of band): the whole relay, as before.
	principalFull
	// principalScoped is a swarm.clients entry: the shared-model routes (the three calls and the probe's ping) of the nodes the entry lists, and nothing else.
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
// rebuilds the relay, so the entries are read live: removing an entry or a name takes effect on the next
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
// A scoped entry is a bearer entry: it proves itself with its token. A token that is also a full token is scoped: least privilege,
// and load refuses the duplicate anyway.
func (s *Server) principalOf(r *http.Request) principal {
	bearer := credentialOf(r)
	clients := s.scopedClients()
	var scoped *config.SwarmClient
	bearerIsScopedToken := false
	for i := range clients {
		c := &clients[i]
		tokenOK := c.Token != "" && bearer != "" && subtle.ConstantTimeCompare([]byte(c.Token), []byte(bearer)) == 1
		if tokenOK {
			bearerIsScopedToken = true
			if scoped == nil {
				scoped = c
			}
		}
	}
	// A bearer that is a full token proves the full class. A bearer that is a token of a scoped entry too (load refuses the duplicate)
	// is scoped, the narrower class.
	if !bearerIsScopedToken && acceptToken(s.clientTokens(), bearer) {
		return principal{class: principalFull}
	}
	if scoped != nil {
		return principal{class: principalScoped, client: scoped}
	}
	return principal{class: principalNone}
}
