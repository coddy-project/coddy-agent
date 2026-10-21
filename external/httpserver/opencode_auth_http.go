//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type openCodeAuthStatusResponse struct {
	Connected    bool   `json:"connected"`
	Account      bool   `json:"account"`
	Organization string `json:"organization,omitempty"`
	Masked       string `json:"masked,omitempty"`
	Source       string `json:"source"`
}

// openCodeDeviceStartRequest names no gateway: the login belongs to the
// account and serves either, as in the OpenCode client.
type openCodeDeviceStartRequest struct {
	Proxy *string `json:"proxy"`
}

func (s *Server) registerOpenCodeAuthRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/opencode-auth", s.coddyProviderOpenCodeAuthGet)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/opencode-auth", s.coddyProviderOpenCodeAuthDelete)
	s.mux.HandleFunc("POST /coddy/providers/{name}/opencode-auth/device", s.coddyProviderOpenCodeAuthDevicePost)
	s.mux.HandleFunc("GET /coddy/providers/{name}/opencode-auth/device/{loginID}", s.coddyProviderOpenCodeAuthDeviceGet)
}

func (s *Server) openCodeAuthStatus(name string, provider config.ProviderConfig) (openCodeAuthStatusResponse, error) {
	status, err := llm.InspectOpenCodeAuth(config.OpenCodeAuthPath(s.activeCfg().Paths.Home, name))
	if err != nil {
		return openCodeAuthStatusResponse{}, err
	}
	response := openCodeAuthStatusResponse{Connected: status.Connected, Account: status.Account, Organization: status.Organization, Masked: status.Masked, Source: "none"}
	switch {
	case strings.TrimSpace(provider.APIKey) != "":
		response.Source = "api_key"
	case strings.TrimSpace(provider.APIKeyCommand) != "":
		response.Source = "api_key_command"
	case strings.TrimSpace(os.Getenv(config.ProviderAPIKeyEnvVarName(name))) != "":
		response.Source = "env"
	case status.Connected && status.Account:
		response.Source = "oauth"
	case status.Connected:
		response.Source = "stored_key"
	}
	return response, nil
}

func (s *Server) coddyProviderOpenCodeAuthGet(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "opencode")
	if !ok {
		return
	}
	response, err := s.openCodeAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, 500, "could not read OpenCode credentials")
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, response)
}

// The attempt lock orders cancellation with persistence, so logout and a newer
// login cannot be followed by an older wait restoring its credential.
func (s *Server) cancelOpenCodeAuthLogins(provider string) {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.opencodeAuthLogins {
		if (provider == "" || attempt.ProviderName == provider) && attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

func (s *Server) coddyProviderOpenCodeAuthDelete(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "opencode")
	if !ok {
		return
	}
	s.cancelOpenCodeAuthLogins(name)
	if err := llm.RemoveOpenCodeAuth(config.OpenCodeAuthPath(s.activeCfg().Paths.Home, name)); err != nil {
		writeCoddyConfigErr(w, 500, "could not remove OpenCode credentials")
		return
	}
	s.providerCredentialChanged(name, "opencode")
	response, err := s.openCodeAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, 500, "could not read OpenCode credentials")
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, response)
}

func (s *Server) coddyProviderOpenCodeAuthDevicePost(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "opencode")
	if !ok {
		return
	}
	var body openCodeDeviceStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, neuralDeepDeviceStartBodyLimit)).Decode(&body); err != nil && err != io.EOF {
		writeCoddyConfigErr(w, 400, "invalid JSON body")
		return
	}
	if err := applySignInProxy(&provider, body.Proxy); err != nil {
		writeCoddyConfigErr(w, 400, err.Error())
		return
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	loginID := newCodexAuthLoginID()
	attempt := &codexAuthLoginAttempt{ProviderName: name, Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	s.codexAuthMu.Lock()
	for id, old := range s.opencodeAuthLogins {
		if old.ProviderName == name || time.Since(old.CreatedAt) > 20*time.Minute {
			if old.cancel != nil {
				old.cancel()
			}
		}
		if time.Since(old.CreatedAt) > 20*time.Minute {
			delete(s.opencodeAuthLogins, id)
		}
	}
	s.opencodeAuthLogins[loginID] = attempt
	s.codexAuthMu.Unlock()
	startCtx, stopStart := context.WithCancel(r.Context())
	defer stopStart()
	stopOnCancel := context.AfterFunc(waitCtx, stopStart)
	defer stopOnCancel()
	login, err := llm.StartOpenCodeDeviceLogin(startCtx, provider.Proxy)
	superseded := waitCtx.Err() != nil
	if err != nil || superseded {
		cancel()
		s.codexAuthMu.Lock()
		delete(s.opencodeAuthLogins, loginID)
		s.codexAuthMu.Unlock()
		if superseded {
			writeCoddyConfigErr(w, 409, "OpenCode sign-in superseded")
			return
		}
		writeCoddyConfigErr(w, 502, llm.RedactOpenCodeSecrets(err.Error()))
		return
	}
	authPath := config.OpenCodeAuthPath(s.activeCfg().Paths.Home, name)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		err := login.WaitWith(waitCtx, func(ctx context.Context, credential []byte) error {
			s.codexAuthMu.Lock()
			defer s.codexAuthMu.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := llm.SaveOpenCodeAuthFile(authPath, credential); err != nil {
				return err
			}
			s.providerCredentialChanged(name, "opencode")
			attempt.Status, attempt.Connected = "completed", true
			return nil
		})
		if err != nil {
			s.codexAuthMu.Lock()
			defer s.codexAuthMu.Unlock()
			if attempt.Status == "pending" {
				attempt.Status, attempt.Error = "failed", llm.RedactOpenCodeSecrets(err.Error())
			}
		}
	}()
	writeCodexAuthJSON(w, 200, codexAuthLoginResponse{LoginID: loginID, VerificationURL: login.URL, UserCode: login.UserCode, Status: "pending"})
}

func (s *Server) coddyProviderOpenCodeAuthDeviceGet(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveSignInProvider(w, r.PathValue("name"), "opencode")
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	attempt := s.opencodeAuthLogins[r.PathValue("loginID")]
	if attempt == nil || attempt.ProviderName != name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 404, "unknown OpenCode login")
		return
	}
	response := codexAuthLoginResponse{Status: attempt.Status, Connected: attempt.Connected, Error: attempt.Error}
	s.codexAuthMu.Unlock()
	writeCodexAuthJSON(w, 200, response)
}
