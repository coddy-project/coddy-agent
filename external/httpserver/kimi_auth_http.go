//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/providerlogin"
)

type kimiAuthRequest struct {
	Proxy    *string `json:"proxy"`
	Region   *string `json:"region"`
	Mode     *string `json:"mode"`
	Identity *string `json:"kimi_identity"`
}

func (s *Server) registerKimiAuthRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/kimi-auth", s.kimiStatus)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/kimi-auth", s.kimiLogout)
	s.mux.HandleFunc("POST /coddy/providers/{name}/kimi-auth/device", s.kimiStart)
	s.mux.HandleFunc("GET /coddy/providers/{name}/kimi-auth/device/{loginID}", s.kimiPoll)
	s.mux.HandleFunc("DELETE /coddy/providers/{name}/kimi-auth/device/{loginID}", s.kimiCancel)
}
func (s *Server) kimiProvider(w http.ResponseWriter, r *http.Request) (string, config.ProviderConfig, bool) {
	name, p, ok := s.resolveSignInProvider(w, r.PathValue("name"), "kimi")
	if !ok {
		return "", p, false
	}
	if q, exists := r.URL.Query()["region"]; exists {
		if len(q) != 1 {
			writeCoddyConfigErr(w, 400, "invalid region")
			return "", p, false
		}
		p.Region = q[0]
	}
	if q, exists := r.URL.Query()["mode"]; exists {
		if len(q) != 1 {
			writeCoddyConfigErr(w, 400, "invalid mode")
			return "", p, false
		}
		p.Mode = q[0]
	}
	if q, exists := r.URL.Query()["kimi_identity"]; exists {
		if len(q) != 1 || (q[0] != "" && !config.ValidKimiIdentity(q[0])) {
			writeCoddyConfigErr(w, 400, "invalid Kimi identity")
			return "", p, false
		}
		p.KimiIdentity = q[0]
	}
	if _, err := llm.KimiCanonicalRegion(p.Region); err != nil {
		writeCoddyConfigErr(w, 400, "invalid region")
		return "", p, false
	}
	if p.Mode != "" && p.Mode != "subscription" && p.Mode != "platform" {
		writeCoddyConfigErr(w, 400, "invalid mode")
		return "", p, false
	}
	return name, p, true
}
func (s *Server) kimiStatus(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.kimiProvider(w, r)
	if !ok {
		return
	}
	source := providerlogin.KimiSource(s.activeCfg(), &p)
	if source == "" {
		source = "none"
	}
	writeCodexAuthJSON(w, 200, map[string]interface{}{"connected": source != "none", "source": source})
}
func (s *Server) kimiLogout(w http.ResponseWriter, r *http.Request) {
	name, p, ok := s.kimiProvider(w, r)
	if !ok {
		return
	}
	if p.Mode != "" && p.Mode != "subscription" {
		writeCoddyConfigErr(w, http.StatusBadRequest, "Kimi logout requires subscription mode")
		return
	}
	dir := config.KimiIdentityDir(s.activeCfg().Paths.Home, p.KimiIdentity)
	if dir == "" {
		writeCoddyConfigErr(w, 400, "Kimi managed identity required")
		return
	}
	if err := s.cancelCodexAuthLoginsFor("kimi:" + name); err != nil {
		writeCoddyConfigErr(w, 500, "could not invalidate Kimi login")
		return
	}
	store := llm.NewKimiCredentialStore(dir)
	if err := store.Remove(p.Region); err != nil {
		writeCoddyConfigErr(w, 500, "could not remove Kimi credential")
		return
	}
	s.providerCredentialChanged(name, "kimi")
	source := providerlogin.KimiSource(s.activeCfg(), &p)
	if source == "" {
		source = "none"
	}
	writeCodexAuthJSON(w, 200, map[string]interface{}{"connected": source != "none", "source": source})
}
func (s *Server) kimiStart(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	name, p, ok := s.kimiProvider(w, r)
	if !ok {
		return
	}
	var original *config.ProviderConfig
	if saved := s.activeCfg().FindProvider(name); saved != nil {
		copy := *saved
		original = &copy
	}
	var body kimiAuthRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, neuralDeepDeviceStartBodyLimit)).Decode(&body); err != nil && err != io.EOF {
		writeCoddyConfigErr(w, 400, "invalid JSON body")
		return
	}
	if body.Region != nil {
		p.Region = *body.Region
	}
	if body.Mode != nil {
		p.Mode = *body.Mode
	}
	if body.Identity != nil {
		p.KimiIdentity = *body.Identity
	}
	if p.KimiIdentity == "" {
		var err error
		p.KimiIdentity, err = config.NewKimiIdentity()
		if err != nil {
			writeCoddyConfigErr(w, 500, "could not create Kimi identity")
			return
		}
	}
	if !config.ValidKimiIdentity(p.KimiIdentity) {
		writeCoddyConfigErr(w, 400, "invalid Kimi identity")
		return
	}
	region, err := llm.KimiCanonicalRegion(p.Region)
	if err != nil {
		writeCoddyConfigErr(w, 400, "invalid region")
		return
	}
	p.Region = region
	if p.Mode != "" && p.Mode != "subscription" {
		writeCoddyConfigErr(w, 400, "device login requires subscription mode")
		return
	}
	if err := applySignInProxy(&p, body.Proxy); err != nil {
		writeCoddyConfigErr(w, 400, "invalid proxy")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := newCodexAuthLoginID()
	a := &codexAuthLoginAttempt{ProviderName: "kimi:" + name, KimiIdentity: p.KimiIdentity, Status: "pending", CreatedAt: time.Now(), cancel: cancel}
	s.codexAuthMu.Lock()
	err = registerAuthLoginAttempt(s.codexAuthLogins, id, a.ProviderName, a)
	s.codexAuthMu.Unlock()
	if err != nil {
		cancel()
		writeCoddyConfigErr(w, 500, "could not invalidate Kimi login")
		return
	}
	startCtx, stop := context.WithCancel(r.Context())
	defer stop()
	stopAfter := context.AfterFunc(ctx, stop)
	defer stopAfter()
	login, err := providerlogin.StartKimiLogin(startCtx, s.activeCfg().Paths.Home, p)
	if err != nil || ctx.Err() != nil {
		superseded := ctx.Err() != nil
		cancel()
		if login != nil {
			if cancelErr := login.Cancel(); cancelErr != nil {
				s.codexAuthMu.Lock()
				a.cancelDurable = login.Cancel
				s.codexAuthMu.Unlock()
				writeCoddyConfigErr(w, 500, "could not invalidate Kimi login")
				return
			}
		}
		s.codexAuthMu.Lock()
		delete(s.codexAuthLogins, id)
		s.codexAuthMu.Unlock()
		if superseded {
			writeCoddyConfigErr(w, 409, "Kimi login superseded")
		} else {
			writeCoddyConfigErr(w, 502, "Kimi device login failed")
		}
		return
	}
	s.codexAuthMu.Lock()
	if ctx.Err() != nil {
		a.cancelDurable = login.Cancel
		if cancelErr := login.Cancel(); cancelErr != nil {
			s.codexAuthMu.Unlock()
			writeCoddyConfigErr(w, 500, "could not invalidate Kimi login")
			return
		}
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 409, "Kimi login superseded")
		return
	}
	a.cancel = cancel
	a.cancelDurable = login.Cancel
	s.codexAuthMu.Unlock()
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer cancel()
		err := login.WaitChecked(ctx, func() error {
			current := s.activeCfg().FindProvider(name)
			if (original == nil) != (current == nil) {
				return errors.New("provider changed")
			}
			if original != nil && (original.Type != current.Type || original.Region != current.Region || original.Mode != current.Mode || original.KimiIdentity != current.KimiIdentity) {
				return errors.New("provider changed")
			}
			return ctx.Err()
		})
		s.codexAuthMu.Lock()
		defer s.codexAuthMu.Unlock()
		if err != nil {
			a.Status = "failed"
			a.Error = "Kimi login failed or was cancelled"
			return
		}
		s.providerCredentialChanged(name, "kimi")
		a.Status = "completed"
		a.Connected = true
	}()
	writeCodexAuthJSON(w, 200, map[string]interface{}{"login_id": id, "verification_url": login.Device.URL, "user_code": login.Device.UserCode, "status": "pending", "connected": false, "kimi_identity": p.KimiIdentity, "region": p.Region, "mode": "subscription"})
}
func (s *Server) kimiPoll(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.kimiProvider(w, r)
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	a := s.codexAuthLogins[r.PathValue("loginID")]
	if a == nil || a.ProviderName != "kimi:"+name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 404, "unknown Kimi login")
		return
	}
	connected := a.Connected
	saved := s.activeCfg().FindProvider(name)
	saveRequired := connected && (saved == nil || a.KimiIdentity == "" || saved.KimiIdentity != a.KimiIdentity || saved.Type != "kimi")
	resp := map[string]interface{}{"status": a.Status, "connected": connected && !saveRequired, "save_required": saveRequired}
	if a.Error != "" {
		resp["error"] = a.Error
	}
	s.codexAuthMu.Unlock()
	writeCodexAuthJSON(w, 200, resp)
}
func (s *Server) kimiCancel(w http.ResponseWriter, r *http.Request) {
	name, _, ok := s.kimiProvider(w, r)
	if !ok {
		return
	}
	s.codexAuthMu.Lock()
	a := s.codexAuthLogins[r.PathValue("loginID")]
	if a == nil || a.ProviderName != "kimi:"+name {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 404, "unknown Kimi login")
		return
	}
	if a.cancelDurable == nil {
		if err := a.stop(); err != nil {
			s.log.Warn("provider login cancellation failed")
		}
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 500, "Kimi login invalidation unavailable")
		return
	}
	if err := a.stop(); err != nil {
		s.codexAuthMu.Unlock()
		writeCoddyConfigErr(w, 500, "could not invalidate Kimi login")
		return
	}
	s.codexAuthMu.Unlock()
	writeCodexAuthJSON(w, 200, map[string]bool{"cancelled": true})
}
