//go:build http

package httpserver

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The SPA signs in to NeuralDeep through the hub's device flow: the browser
// and this server may run on different machines, so the CLI's loopback
// callback cannot be received here. Login attempts reuse the codex attempt
// bookkeeping (same mutex, same drain semantics).

type neuralDeepAuthStatusResponse struct {
	Connected bool   `json:"connected"`
	Masked    string `json:"masked,omitempty"`
	KeyName   string `json:"key_name,omitempty"`
	// Source names the credential requests will actually use:
	// "oauth", "api_key", "api_key_command", "env", or "none". The SPA warns
	// when an explicit key shadows a stored login.
	Source string `json:"source"`
}

// cancelNeuralDeepAuthLogins stops every sign-in still waiting for approval.
func (s *Server) cancelNeuralDeepAuthLogins() {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.neuralDeepAuthLogins {
		if attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

// cancelNeuralDeepAuthLoginsFor stops the pending sign-ins of one provider.
// A new login supersedes the previous one, and a sign-out must not leave a
// background wait that later re-stores a credential the user just removed.
func (s *Server) cancelNeuralDeepAuthLoginsFor(provider string) {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.neuralDeepAuthLogins {
		if attempt.ProviderName == provider && attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

func (s *Server) registerNeuralDeepAuthRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/neuraldeep-auth", s.coddyProviderNeuralDeepAuthGet)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/neuraldeep-auth", s.coddyProviderNeuralDeepAuthDelete)
	s.mux.HandleFunc("POST /coddy/providers/{name}/neuraldeep-auth/device", s.coddyProviderNeuralDeepAuthDevicePost)
	s.mux.HandleFunc("GET /coddy/providers/{name}/neuraldeep-auth/device/{loginID}", s.coddyProviderNeuralDeepAuthDeviceGet)
}

func (s *Server) neuralDeepAuthStatus(name string, provider config.ProviderConfig) (neuralDeepAuthStatusResponse, error) {
	st, err := llm.InspectNeuralDeepAuth(config.NeuralDeepAuthPath(s.activeCfg().Paths.Home, name))
	if err != nil {
		return neuralDeepAuthStatusResponse{}, err
	}
	resp := neuralDeepAuthStatusResponse{
		Connected: st.Connected,
		Masked:    st.Masked,
		KeyName:   st.KeyName,
		Source:    "none",
	}
	switch {
	case strings.TrimSpace(provider.APIKey) != "":
		resp.Source = "api_key"
	case strings.TrimSpace(provider.APIKeyCommand) != "":
		resp.Source = "api_key_command"
	case strings.TrimSpace(os.Getenv(config.ProviderAPIKeyEnvVarName(name))) != "":
		resp.Source = "env"
	case st.Connected:
		resp.Source = "oauth"
	}
	return resp, nil
}

func (s *Server) coddyProviderNeuralDeepAuthGet(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveNeuralDeepAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	resp, err := s.neuralDeepAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, resp)
}

func (s *Server) coddyProviderNeuralDeepAuthDelete(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveNeuralDeepAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	// A background device wait finishing after the sign-out would silently
	// re-store a credential; supersede every pending attempt first.
	s.cancelNeuralDeepAuthLoginsFor(name)
	path := config.NeuralDeepAuthPath(s.activeCfg().Paths.Home, name)
	// Honest logout: ask the hub to revoke the key first, best-effort. A hub
	// that is unreachable must not keep the user locked in.
	if key, err := llm.LoadNeuralDeepKey(path); err == nil && key != "" {
		st, _ := llm.InspectNeuralDeepAuth(path)
		hub := st.Hub
		if hub == "" {
			hub = llm.NeuralDeepHub()
		}
		client, _ := llm.HTTPClientForOptionalProxy(provider.Proxy)
		revokeCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		_ = llm.RevokeNeuralDeepKey(revokeCtx, hub, key, client)
		cancel()
	}
	if err := llm.RemoveNeuralDeepAuth(path); err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "could not remove NeuralDeep credentials")
		return
	}
	resp, err := s.neuralDeepAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, resp)
}

func (s *Server) coddyProviderNeuralDeepAuthDevicePost(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveNeuralDeepAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	client, err := llm.HTTPClientForOptionalProxy(provider.Proxy)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hub := llm.NeuralDeepHub()
	label := neuralDeepHTTPDeviceLabel()
	// Two racing sign-ins would finish in arbitrary order and the loser
	// could overwrite the newer credential; the new attempt supersedes.
	s.cancelNeuralDeepAuthLoginsFor(name)
	login, err := llm.StartNeuralDeepDeviceLogin(r.Context(), hub, client, label)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadGateway, err.Error())
		return
	}
	loginID := newCodexAuthLoginID()
	// The wait outlives this request but not the server: Drain cancels it.
	waitCtx, cancel := context.WithCancel(context.Background())
	attempt := &codexAuthLoginAttempt{ProviderName: name, Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	s.codexAuthMu.Lock()
	for id, old := range s.neuralDeepAuthLogins {
		if time.Since(old.CreatedAt) > 20*time.Minute {
			if old.cancel != nil {
				old.cancel()
			}
			delete(s.neuralDeepAuthLogins, id)
		}
	}
	s.neuralDeepAuthLogins[loginID] = attempt
	s.codexAuthMu.Unlock()

	authPath := config.NeuralDeepAuthPath(s.activeCfg().Paths.Home, name)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		_, err := llm.CompleteNeuralDeepDeviceLogin(waitCtx, hub, client, login, authPath, label)
		s.codexAuthMu.Lock()
		defer s.codexAuthMu.Unlock()
		if err != nil {
			attempt.Status = "failed"
			attempt.Error = err.Error()
			return
		}
		attempt.Status = "completed"
		attempt.Connected = true
	}()

	writeCodexAuthJSON(w, http.StatusOK, codexAuthLoginResponse{
		LoginID: loginID,
		// The complete URI (pre-filled code) when the hub provides one,
		// otherwise the plain portal URI - complete is optional in RFC 8628.
		VerificationURL: login.VerificationTarget(),
		UserCode:        login.UserCode,
		Status:          "pending",
	})
}

func (s *Server) coddyProviderNeuralDeepAuthDeviceGet(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveNeuralDeepAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	attempt := s.neuralDeepAuthLogins[r.PathValue("loginID")]
	if attempt == nil || attempt.ProviderName != name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown NeuralDeep login")
		return
	}
	response := codexAuthLoginResponse{
		Status:    attempt.Status,
		Connected: attempt.Connected,
		Error:     attempt.Error,
	}
	s.codexAuthMu.Unlock()
	writeCodexAuthJSON(w, http.StatusOK, response)
}

// resolveNeuralDeepAuthProvider accepts saved neuraldeep providers and valid
// unsaved names, so a provider added in the settings form can sign in before
// the document is saved (same convention as codex).
func (s *Server) resolveNeuralDeepAuthProvider(w http.ResponseWriter, rawName string) (string, config.ProviderConfig, bool) {
	c := s.activeCfg()
	if c == nil || strings.TrimSpace(c.Paths.Home) == "" {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config home unavailable")
		return "", config.ProviderConfig{}, false
	}
	name := strings.TrimSpace(rawName)
	probe := config.ProviderConfig{Name: name, Type: "neuraldeep"}
	probe.Normalize()
	if err := probe.Validate(); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return "", config.ProviderConfig{}, false
	}
	if saved := c.FindProvider(name); saved != nil {
		if saved.Type != "neuraldeep" {
			writeCoddyConfigErr(w, http.StatusConflict, "provider is not a NeuralDeep provider")
			return "", config.ProviderConfig{}, false
		}
		return name, *saved, true
	}
	return name, probe, true
}

func neuralDeepHTTPDeviceLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "coddy"
	}
	return "coddy @ " + host
}
