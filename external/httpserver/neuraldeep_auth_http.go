//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// Hub is where the stored login was issued (recorded in the credential
	// file); EndpointHub is the hub a sign-in for the queried api_base would
	// use. The SPA warns when they differ, since a key minted by one
	// deployment is not honored by the other.
	Hub         string `json:"hub,omitempty"`
	EndpointHub string `json:"endpoint_hub,omitempty"`
}

// neuralDeepDeviceStartRequest is the optional body of the device start: the
// endpoint picked in the settings form, which may not be saved yet.
type neuralDeepDeviceStartRequest struct {
	APIBase string `json:"api_base"`
}

// neuralDeepDeviceStartBodyLimit bounds the optional JSON body.
const neuralDeepDeviceStartBodyLimit = 4 << 10

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

// neuralDeepAuthStatus reports the stored login for provider; apiBase is the
// endpoint the caller is interested in (the settings form's current pick),
// falling back to the saved row so EndpointHub always names a hub.
func (s *Server) neuralDeepAuthStatus(name string, provider config.ProviderConfig, apiBase string) (neuralDeepAuthStatusResponse, error) {
	st, err := llm.InspectNeuralDeepAuth(config.NeuralDeepAuthPath(s.activeCfg().Paths.Home, name))
	if err != nil {
		return neuralDeepAuthStatusResponse{}, err
	}
	if strings.TrimSpace(apiBase) == "" {
		apiBase = provider.APIBase
	}
	resp := neuralDeepAuthStatusResponse{
		Connected:   st.Connected,
		Masked:      st.Masked,
		KeyName:     st.KeyName,
		Source:      "none",
		Hub:         st.Hub,
		EndpointHub: s.neuralDeepHubFor(apiBase),
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
	// ?api_base= is the endpoint picked in the form. A value that names no
	// NeuralDeep endpoint counts as omitted: the answer then describes the
	// saved row (which itself falls back to the default deployment, like
	// requests do) rather than failing the status read.
	apiBase, _ := llm.NormalizeNeuralDeepAPIBase(r.URL.Query().Get("api_base"))
	resp, err := s.neuralDeepAuthStatus(name, provider, apiBase)
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
			hub = s.neuralDeepHubFor(provider.APIBase)
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
	resp, err := s.neuralDeepAuthStatus(name, provider, "")
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
	apiBase, err := neuralDeepDeviceStartEndpoint(r, provider)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hub := s.neuralDeepHubFor(apiBase)
	label := neuralDeepHTTPDeviceLabel()
	loginID := newCodexAuthLoginID()
	// The wait outlives this request but not the server: Drain cancels it.
	waitCtx, cancel := context.WithCancel(context.Background())
	attempt := &codexAuthLoginAttempt{ProviderName: name, Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	// Two racing sign-ins would finish in arbitrary order and the loser
	// could overwrite the newer credential; the new attempt supersedes. It is
	// registered before the hub is contacted, so a start still in flight is
	// already visible to a concurrent start or sign-out and gets cancelled
	// with everything else instead of slipping through the gap.
	s.codexAuthMu.Lock()
	for id, old := range s.neuralDeepAuthLogins {
		if old.ProviderName == name || time.Since(old.CreatedAt) > 20*time.Minute {
			if old.cancel != nil {
				old.cancel()
			}
		}
		if time.Since(old.CreatedAt) > 20*time.Minute {
			delete(s.neuralDeepAuthLogins, id)
		}
	}
	s.neuralDeepAuthLogins[loginID] = attempt
	s.codexAuthMu.Unlock()

	// The hub call answers this request, so it follows the request context,
	// but a supersede or sign-out in the meantime aborts it as well.
	startCtx, stopStart := context.WithCancel(r.Context())
	defer stopStart()
	stopOnCancel := context.AfterFunc(waitCtx, stopStart)
	defer stopOnCancel()
	login, err := llm.StartNeuralDeepDeviceLogin(startCtx, hub, client, label)
	if err != nil {
		cancel()
		s.codexAuthMu.Lock()
		delete(s.neuralDeepAuthLogins, loginID)
		s.codexAuthMu.Unlock()
		if waitCtx.Err() != nil {
			writeCoddyConfigErr(w, http.StatusConflict, "NeuralDeep sign-in superseded before the hub answered")
			return
		}
		writeCoddyConfigErr(w, http.StatusBadGateway, err.Error())
		return
	}

	authPath := config.NeuralDeepAuthPath(s.activeCfg().Paths.Home, name)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		_, err := llm.CompleteNeuralDeepDeviceLoginWith(waitCtx, hub, client, login, func(ctx context.Context, key string) error {
			return s.persistNeuralDeepLogin(ctx, attempt, authPath, key, hub, label)
		})
		if err == nil {
			return
		}
		s.codexAuthMu.Lock()
		defer s.codexAuthMu.Unlock()
		if attempt.Status == "pending" {
			attempt.Status = "failed"
			attempt.Error = err.Error()
		}
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

// persistNeuralDeepLogin stores the key a device login minted and marks the
// attempt completed, both under the attempt lock: a sign-out or a newer login
// cancels attempts under the same lock, so the cancellation check here cannot
// be overtaken by a removal that has already happened, and a cancelled
// attempt never resurrects a credential the user just removed.
func (s *Server) persistNeuralDeepLogin(ctx context.Context, attempt *codexAuthLoginAttempt, authPath, key, hub, label string) error {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("neuraldeep auth: login cancelled before the key was stored: %w", err)
	}
	if err := llm.SaveNeuralDeepAuth(authPath, key, hub, llm.NeuralDeepClientID, label); err != nil {
		return err
	}
	attempt.Status = "completed"
	attempt.Connected = true
	return nil
}

// neuralDeepDeviceStartEndpoint settles which deployment a device sign-in is
// for: the api_base in the optional JSON body (the settings form's current
// pick, possibly unsaved), else the saved row. A body value outside the
// allowlist is refused up front - the hub it would resolve to is the default
// one, and a key minted there is useless on the endpoint the user picked.
func neuralDeepDeviceStartEndpoint(r *http.Request, provider config.ProviderConfig) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, neuralDeepDeviceStartBodyLimit))
	if err != nil {
		return "", fmt.Errorf("read request body: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return provider.APIBase, nil
	}
	var body neuralDeepDeviceStartRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("invalid JSON body: %w", err)
	}
	if strings.TrimSpace(body.APIBase) == "" {
		return provider.APIBase, nil
	}
	base, ok := llm.NormalizeNeuralDeepAPIBase(body.APIBase)
	if !ok {
		return "", fmt.Errorf("api_base %q is not a NeuralDeep endpoint; use one of %s",
			strings.TrimSpace(body.APIBase), strings.Join(llm.NeuralDeepAPIBases(), ", "))
	}
	return base, nil
}

func neuralDeepHTTPDeviceLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "coddy"
	}
	return "coddy @ " + host
}
