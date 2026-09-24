//go:build http

package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type codexAuthLoginAttempt struct {
	ProviderName string
	Status       string
	Connected    bool
	Error        string
	CreatedAt    time.Time
	// cancel stops the waiting goroutine when the server drains.
	cancel context.CancelFunc
}

type codexAuthLoginResponse struct {
	LoginID         string `json:"login_id,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	Status          string `json:"status,omitempty"`
	Connected       bool   `json:"connected"`
	Error           string `json:"error,omitempty"`
}

// cancelCodexAuthLogins stops every sign-in still waiting for confirmation.
// Called from Drain so shutdown does not wait out the device-flow timeout.
func (s *Server) cancelCodexAuthLogins() {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.codexAuthLogins {
		if attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

// cancelCodexAuthLoginsFor stops the pending sign-ins of one provider. A new
// login supersedes the previous one, and a sign-out must not leave a
// background wait that later re-stores a credential the user just removed.
func (s *Server) cancelCodexAuthLoginsFor(provider string) {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	for _, attempt := range s.codexAuthLogins {
		if attempt.ProviderName == provider && attempt.cancel != nil {
			attempt.cancel()
		}
	}
}

func (s *Server) registerCodexAuthRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/codex-auth", s.coddyProviderCodexAuthGet)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/codex-auth", s.coddyProviderCodexAuthDelete)
	s.mux.HandleFunc("POST /coddy/providers/{name}/codex-auth/device", s.coddyProviderCodexAuthDevicePost)
	s.mux.HandleFunc("GET /coddy/providers/{name}/codex-auth/device/{loginID}", s.coddyProviderCodexAuthDeviceGet)
}

// codexAuthStatusResponse is the Codex sign-in status of a row, plus the row
// the Codex CLI login on this server serves when this row may not use it, so
// Settings can say why a row is not signed in while a CLI login exists.
type codexAuthStatusResponse struct {
	llm.CodexAuthStatus
	CLILoginRow string `json:"cli_login_row,omitempty"`
}

// codexAuthStatus inspects the credential of the row named name; the Codex CLI
// login counts only for the row config.Config.CLILoginRow names.
func (s *Server) codexAuthStatus(name string) (codexAuthStatusResponse, error) {
	c := s.activeCfg()
	cliLogin := c.ProviderMayUseCLILogin(name, "codex")
	st, err := llm.InspectCodexAuth(config.CodexAuthPath(c.Paths.Home, name), cliLogin)
	if err != nil {
		return codexAuthStatusResponse{}, err
	}
	resp := codexAuthStatusResponse{CodexAuthStatus: st}
	if !cliLogin && !st.Connected && llm.CodexCLILoginPresent() {
		resp.CLILoginRow = c.CLILoginRow("codex", name)
	}
	return resp, nil
}

func (s *Server) coddyProviderCodexAuthGet(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveCodexAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	status, err := s.codexAuthStatus(name)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, status)
}

func (s *Server) coddyProviderCodexAuthDelete(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveCodexAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	// A background device wait finishing after the sign-out would silently
	// re-store the credential; supersede every pending attempt first.
	s.cancelCodexAuthLoginsFor(name)
	path := config.CodexAuthPath(s.activeCfg().Paths.Home, name)
	if err := llm.RemoveCodexAuth(path); err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "could not remove Codex credentials")
		return
	}
	// The account the cached usage described is gone; a stale snapshot must
	// not outlive the credential.
	s.dropProviderUsage(name, "codex")
	status, err := s.codexAuthStatus(name)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeCodexAuthJSON(w, http.StatusOK, status)
}

func (s *Server) coddyProviderCodexAuthDevicePost(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	name, provider, ok := s.resolveCodexAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	client, err := llm.HTTPClientForProviderProxy(provider.Proxy)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return
	}
	loginID := newCodexAuthLoginID()
	// The wait outlives this request but not the server: Drain cancels it, so a
	// sign-in nobody confirms cannot keep polling or write into a home directory
	// the caller has already torn down.
	waitCtx, cancel := context.WithCancel(context.Background())
	attempt := &codexAuthLoginAttempt{ProviderName: name, Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	// Two racing sign-ins would finish in arbitrary order and the loser could
	// overwrite the newer credential; the new attempt supersedes. It is
	// registered before the issuer is contacted, so a start still in flight is
	// already visible to a concurrent start or sign-out and gets cancelled
	// with everything else instead of slipping through the gap.
	s.codexAuthMu.Lock()
	for id, old := range s.codexAuthLogins {
		if old.ProviderName == name || time.Since(old.CreatedAt) > 20*time.Minute {
			if old.cancel != nil {
				old.cancel()
			}
		}
		if time.Since(old.CreatedAt) > 20*time.Minute {
			delete(s.codexAuthLogins, id)
		}
	}
	s.codexAuthLogins[loginID] = attempt
	s.codexAuthMu.Unlock()

	// The issuer call answers this request, so it follows the request context,
	// but a supersede or sign-out in the meantime aborts it as well.
	startCtx, stopStart := context.WithCancel(r.Context())
	defer stopStart()
	stopOnCancel := context.AfterFunc(waitCtx, stopStart)
	defer stopOnCancel()
	login, err := llm.StartCodexDeviceLogin(startCtx, s.codexAuthIssuer, client)
	// Read before cancel(): afterwards waitCtx is always done and a plain
	// issuer failure would masquerade as a supersede.
	superseded := waitCtx.Err() != nil
	if err != nil || superseded {
		cancel()
		s.codexAuthMu.Lock()
		delete(s.codexAuthLogins, loginID)
		s.codexAuthMu.Unlock()
		if superseded {
			// Cancelled while the issuer was answering, or right after it
			// did: a newer start or a sign-out owns the provider now.
			writeCoddyConfigErr(w, http.StatusConflict, "Codex sign-in superseded before the issuer answered")
			return
		}
		writeCoddyConfigErr(w, http.StatusBadGateway, err.Error())
		return
	}

	authPath := config.CodexAuthPath(s.activeCfg().Paths.Home, name)
	issuer := s.codexAuthIssuer
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		err := llm.CompleteCodexDeviceLoginWith(waitCtx, issuer, client, login, func(ctx context.Context, credential []byte) error {
			return s.persistCodexLogin(ctx, attempt, authPath, credential)
		})
		if err == nil {
			// The account changed: any cached usage describes the previous
			// sign-in and must be re-read.
			s.dropProviderUsage(name, "codex")
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
		LoginID:         loginID,
		VerificationURL: login.VerificationURL,
		UserCode:        login.UserCode,
		Status:          "pending",
	})
}

// persistCodexLogin stores the credential a device login obtained and marks
// the attempt completed, both under the attempt lock: a sign-out or a newer
// login cancels attempts under the same lock, so the cancellation check here
// cannot be overtaken by a removal that has already happened, and a
// cancelled attempt never resurrects a credential the user just removed.
func (s *Server) persistCodexLogin(ctx context.Context, attempt *codexAuthLoginAttempt, authPath string, credential []byte) error {
	s.codexAuthMu.Lock()
	defer s.codexAuthMu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("codex auth: login cancelled before the credential was stored: %w", err)
	}
	if err := llm.SaveCodexAuthFile(authPath, credential); err != nil {
		return err
	}
	attempt.Status = "completed"
	attempt.Connected = true
	return nil
}

func (s *Server) coddyProviderCodexAuthDeviceGet(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.resolveCodexAuthProvider(w, r.PathValue("name"))
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	attempt := s.codexAuthLogins[r.PathValue("loginID")]
	if attempt == nil || attempt.ProviderName != name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown Codex login")
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

// resolveCodexAuthProvider accepts saved Codex providers, valid unsaved names
// and saved rows the settings form is switching to codex, so a row can be
// signed in before the settings document is saved.
func (s *Server) resolveCodexAuthProvider(w http.ResponseWriter, rawName string) (string, config.ProviderConfig, bool) {
	return s.resolveSignInProvider(w, rawName, "codex")
}

// resolveSignInProvider settles which row a Settings sign-in acts for. The
// form signs a row in before it is saved, so neither the name nor the type
// has to be saved yet: a new row has no saved entry, and a saved row whose
// type picker was just switched (config.example.yaml ships one named
// "openai") still carries its old type. A saved row of providerType is used
// as saved. Any other name gets a probe of providerType that keeps only the
// saved row's proxy: the route survives a type switch in the form, while the
// endpoint and the credentials belong to the other type and would mislead
// the sign-in.
func (s *Server) resolveSignInProvider(w http.ResponseWriter, rawName, providerType string) (string, config.ProviderConfig, bool) {
	c := s.activeCfg()
	if c == nil || strings.TrimSpace(c.Paths.Home) == "" {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config home unavailable")
		return "", config.ProviderConfig{}, false
	}
	name := strings.TrimSpace(rawName)
	probe := config.ProviderConfig{Name: name, Type: providerType}
	probe.Normalize()
	if err := probe.Validate(); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return "", config.ProviderConfig{}, false
	}
	if saved := c.FindProvider(name); saved != nil {
		if saved.Type == providerType {
			return name, *saved, true
		}
		probe.Proxy = saved.Proxy
	}
	return name, probe, true
}

// requireJSONRequest refuses a request body that is not application/json
// with 415. A page on another site can make a browser send a POST to a
// loopback server without a preflight only as a "simple" request (text/plain
// or a form encoding); a route that starts work on the server, such as a
// sign-in that supersedes the pending one or runs a credential helper, must
// not be reachable that way.
func requireJSONRequest(w http.ResponseWriter, r *http.Request) bool {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeCoddyConfigErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

func newCodexAuthLoginID() string {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func writeCodexAuthJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
