//go:build http

package httpserver

import (
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
			s.mgr.ReplaceConfig(c)
			return nil
		},
		Schema:    func() ([]byte, error) { return config.UISchemaJSONHiding(s.unbuiltSections...) },
		Decorate:  s.decorateConfigDocument,
		Revisions: s.served,
		Log:       s.log,
	}
	backend.Register(s.mux)
}

// SetUnbuiltSections names the settings sections the form must not offer
// because this binary cannot run them. They stay in the settings document, so
// a save keeps them in the file.
func (s *Server) SetUnbuiltSections(sections []string) {
	s.unbuiltSections = append([]string(nil), sections...)
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
