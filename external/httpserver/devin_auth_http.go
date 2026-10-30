//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type devinAuthLoginAttempt struct {
	ProviderName string
	Status       string
	Connected    bool
	Error        string
	CreatedAt    time.Time
	cancel       context.CancelFunc
	paste        *io.PipeWriter
}

type devinAuthStatusResponse struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source"`
	Masked    string `json:"masked,omitempty"`
	Email     string `json:"email,omitempty"`
}

type devinDeviceStartRequest struct {
	Proxy *string `json:"proxy"`
}

func (s *Server) registerDevinAuthRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/devin-auth", s.coddyProviderDevinAuthGet)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/devin-auth", s.coddyProviderDevinAuthDelete)
	s.mux.HandleFunc("POST /coddy/providers/{name}/devin-auth/device", s.coddyProviderDevinAuthDevicePost)
	s.mux.HandleFunc("GET /coddy/providers/{name}/devin-auth/device/{loginID}", s.coddyProviderDevinAuthDeviceGet)
	s.mux.HandleFunc("POST /coddy/providers/{name}/devin-auth/device/{loginID}/paste", s.coddyProviderDevinAuthPastePost)
}

func (s *Server) devinAuthStatus(name string, provider config.ProviderConfig) (devinAuthStatusResponse, error) {
	c := s.activeCfg()
	st, err := llm.InspectDevinAuth(config.DevinAuthPath(c.Paths.Home, name), c.ProviderMayUseCLILogin(name, "devin"))
	if err != nil {
		return devinAuthStatusResponse{}, err
	}
	response := devinAuthStatusResponse{Connected: st.Connected, Source: st.Source, Masked: st.Masked, Email: st.Email}
	if response.Source == "" {
		response.Source = "none"
	}
	switch {
	case strings.TrimSpace(provider.APIKey) != "":
		response.Connected, response.Source = true, "api_key"
	case strings.TrimSpace(provider.APIKeyCommand) != "":
		response.Connected, response.Source = true, "api_key_command"
	case strings.TrimSpace(os.Getenv(config.ProviderAPIKeyEnvVarName(name))) != "":
		response.Connected, response.Source = true, "env"
	}
	return response, nil
}

func (s *Server) coddyProviderDevinAuthGet(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "devin")
	if !ok {
		return
	}
	status, err := s.devinAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "could not read Devin credentials")
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, status)
}

func (s *Server) cancelDevinAuthLogins(provider string) {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.devinAuthLogins {
		if provider == "" || attempt.ProviderName == provider {
			if attempt.cancel != nil {
				attempt.cancel()
			}
			if attempt.paste != nil {
				_ = attempt.paste.Close()
			}
		}
	}
}

func (s *Server) coddyProviderDevinAuthDelete(w http.ResponseWriter, r *http.Request) {
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "devin")
	if !ok {
		return
	}
	s.cancelDevinAuthLogins(name)
	if err := llm.RemoveDevinAuth(config.DevinAuthPath(s.activeCfg().Paths.Home, name)); err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "could not remove Devin credentials")
		return
	}
	s.providerCredentialChanged(name, "devin")
	status, err := s.devinAuthStatus(name, provider)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "could not read Devin credentials")
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, status)
}

func (s *Server) coddyProviderDevinAuthDevicePost(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	name, provider, ok := s.resolveSignInProvider(w, r.PathValue("name"), "devin")
	if !ok {
		return
	}
	var body devinDeviceStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil && err != io.EOF {
		writeCoddyConfigErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := applySignInProxy(&provider, body.Proxy); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	loginID := newCodexAuthLoginID()
	pr, pw := io.Pipe()
	attempt := &devinAuthLoginAttempt{ProviderName: name, Status: "pending", CreatedAt: time.Now(), cancel: cancel, paste: pw}
	s.codexAuthMu.Lock()
	for id, old := range s.devinAuthLogins {
		if old.ProviderName == name || time.Since(old.CreatedAt) > 20*time.Minute {
			if old.cancel != nil {
				old.cancel()
			}
			if old.paste != nil {
				_ = old.paste.Close()
			}
		}
		if time.Since(old.CreatedAt) > 20*time.Minute {
			delete(s.devinAuthLogins, id)
		}
	}
	s.devinAuthLogins[loginID] = attempt
	s.codexAuthMu.Unlock()

	authPath := config.DevinAuthPath(s.activeCfg().Paths.Home, name)
	promptCh := make(chan llm.DevinLoginPrompt, 1)
	doneCh := make(chan error, 1)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		defer func() { _ = pr.Close(); _ = pw.Close() }()
		_, err := llm.DevinSignIn(waitCtx, provider.Proxy, authPath, llm.DevinSignInOptions{
			OnPrompt: func(prompt llm.DevinLoginPrompt) { promptCh <- prompt },
			Paste:    pr,
			OnPasteRejected: func(reason string) {
				s.codexAuthMu.Lock()
				defer s.codexAuthMu.Unlock()
				if attempt.Status == "pending" {
					attempt.Error = reason
				}
			},
			BeforeStore: func() (func(), error) {
				s.codexAuthMu.Lock()
				if err := waitCtx.Err(); err != nil {
					s.codexAuthMu.Unlock()
					return nil, err
				}
				if s.devinAuthLogins[loginID] != attempt {
					s.codexAuthMu.Unlock()
					return nil, errors.New("devin sign-in superseded")
				}
				return s.codexAuthMu.Unlock, nil
			},
		})
		s.codexAuthMu.Lock()
		attempt.paste = nil
		switch {
		case err != nil:
			if attempt.Status == "pending" {
				attempt.Status, attempt.Error = "failed", err.Error()
			}
		case waitCtx.Err() != nil:
			if attempt.Status == "pending" {
				attempt.Status, attempt.Error = "failed", waitCtx.Err().Error()
			}
		default:
			attempt.Status, attempt.Connected, attempt.Error = "completed", true, ""
			s.providerCredentialChanged(name, "devin")
		}
		s.codexAuthMu.Unlock()
		doneCh <- err
	}()

	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case prompt := <-promptCh:
		writeCodexAuthJSON(w, http.StatusOK, codexAuthLoginResponse{LoginID: loginID, VerificationURL: prompt.AuthURL, Status: "pending"})
	case err := <-doneCh:
		cancel()
		if err == nil {
			err = errors.New("devin sign-in ended before its browser prompt")
		}
		writeCoddyConfigErr(w, http.StatusBadGateway, err.Error())
	case <-r.Context().Done():
		cancel()
	case <-timer.C:
		cancel()
		writeCoddyConfigErr(w, http.StatusGatewayTimeout, "Devin sign-in did not start")
	}
}

func (s *Server) coddyProviderDevinAuthDeviceGet(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveSignInProvider(w, r.PathValue("name"), "devin")
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	attempt := s.devinAuthLogins[r.PathValue("loginID")]
	if attempt == nil || attempt.ProviderName != name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown Devin login")
		return
	}
	response := codexAuthLoginResponse{Status: attempt.Status, Connected: attempt.Connected, Error: attempt.Error}
	s.codexAuthMu.Unlock()
	writeCodexAuthJSON(w, http.StatusOK, response)
}

func (s *Server) coddyProviderDevinAuthPastePost(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	name, _, ok := s.resolveSignInProvider(w, r.PathValue("name"), "devin")
	if !ok {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeCoddyConfigErr(w, http.StatusBadRequest, "url is required")
		return
	}
	s.codexAuthMu.Lock()
	attempt := s.devinAuthLogins[r.PathValue("loginID")]
	if attempt == nil || attempt.ProviderName != name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown Devin login")
		return
	}
	if attempt.Status != "pending" || attempt.paste == nil {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, http.StatusConflict, "Devin login is not waiting for a callback")
		return
	}
	paste := attempt.paste
	attempt.Error = ""
	s.codexAuthMu.Unlock()
	if _, err := io.WriteString(paste, strings.TrimSpace(body.URL)+"\n"); err != nil {
		writeCoddyConfigErr(w, http.StatusConflict, "Devin login is no longer waiting for a callback")
		return
	}
	writeCodexAuthJSON(w, http.StatusAccepted, codexAuthLoginResponse{Status: "pending"})
}
