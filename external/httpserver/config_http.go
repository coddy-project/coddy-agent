//go:build http

package httpserver

import (
	"bytes"
	"io"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/configapi"
)

// registerConfigRoutes mounts the settings form's routes (internal/configapi,
// shared with the swarm relay) and the reasoning levels lookup it uses.
func (s *Server) registerConfigRoutes() {
	s.mux.HandleFunc("GET /coddy/config/reasoning-levels", s.coddyConfigReasoningLevelsGet)
	backend := &configapi.Backend{
		Live: s.activeCfg,
		// ReplaceConfig on the manager reaches this server through the config
		// observer registered in New, which is also how a reload from the
		// agent's own config_commit tool or from the console gets here.
		Install: func(c *config.Config) error {
			// The last line of defence: a shared-model token that is also a main
			// or swarm token is refused here too, with the flag and environment
			// tokens in view, which the loader the save went through cannot see.
			if err := s.checkTokenClasses(c); err != nil {
				return err
			}
			s.mgr.ReplaceConfig(c)
			return nil
		},
		Schema:    config.UISchemaJSON,
		Decorate:  s.decorateConfigDocument,
		Revisions: s.served,
		Log:       s.log,
	}
	// The backend serves on a mux of its own, so that the two routes which take a
	// candidate configuration can be checked before the backend touches the file:
	// a save that fails after the write cannot be undone from here, because every
	// successful load refreshes the backup the rollback restores.
	inner := http.NewServeMux()
	backend.Register(inner)
	s.mux.Handle("GET /coddy/config/schema", inner)
	s.mux.Handle("GET /coddy/config", inner)
	s.mux.Handle("POST /coddy/config/validate", s.refuseTokenClassClash(inner))
	s.mux.Handle("PUT /coddy/config", s.refuseTokenClassClash(inner))
}

// maxConfigBodyBytes bounds the settings document the check below reads ahead.
const maxConfigBodyBytes = 8 << 20

// refuseTokenClassClash answers a candidate configuration that would make a
// shared-model token the same as a main-class or swarm token - the ones this
// process was given by flag or environment included, which the loader cannot
// see - before anything is written: a refused candidate leaves both the running
// configuration and the file as they were. A document that does not parse is
// left to the backend, which reports it in its own words.
func (s *Server) refuseTokenClassClash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBodyBytes))
		if err != nil {
			writeCoddyConfigErr(w, http.StatusBadRequest, "read body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if live := s.activeCfg(); live != nil {
			if candidate, perr := config.ParseConfigJSONPreservingSecrets(body, live.Paths, live); perr == nil {
				if cerr := s.checkTokenClasses(candidate); cerr != nil {
					writeCoddyConfigErr(w, http.StatusBadRequest, cerr.Error())
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// decorateConfigDocument reports the effective auth state (YAML token or
// out-of-band --auth-token / CODDY_HTTP_TOKEN), not just the config-file token,
// so the UI can reflect that auth is on regardless of source.
func (s *Server) decorateConfigDocument(dto *config.ConfigJSON) {
	pol := s.authPolicyNow()
	dto.HTTPServer.AuthConfigured = len(pol.tokens) > 0
	// The sign-in form is reported the same way and for the same reason: the
	// account may come from the environment, which is nowhere in this document.
	dto.HTTPServer.LoginConfigured = pol.login.enabled && !pol.login.broken
	if dto.HTTPServer.LoginConfigured {
		dto.HTTPServer.LoginSource = pol.login.account.source
	}
	// login.user stays whatever the file says, even when the live account came
	// from the environment: this document is what a save writes back, and an
	// environment credential must never end up in it. Who is signed in is
	// GET /coddy/auth/me's answer, not this one's.
}

// writeCoddyConfigErr answers with the error document of the config routes.
func writeCoddyConfigErr(w http.ResponseWriter, code int, msg string) {
	configapi.WriteError(w, code, msg)
}
