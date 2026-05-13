//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type adminProvider struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key,omitempty"`
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "****"
	}
	return "..." + k[len(k)-4:]
}

func (s *Server) handleAdminProvidersGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	out := []adminProvider{}
	if s.cfg != nil && s.cfg.RuntimeOverlay != nil {
		for _, p := range s.cfg.RuntimeOverlay.Providers {
			out = append(out, adminProvider{
				Name:    p.Name,
				Type:    p.Type,
				APIBase: p.APIBase,
				APIKey:  maskKey(p.APIKey),
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleAdminProviderPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req adminProvider
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.TrimSpace(req.Type)
	req.APIBase = strings.TrimSpace(req.APIBase)
	req.APIKey = strings.TrimSpace(req.APIKey)

	if req.Name == "" {
		http.Error(w, `{"error":{"message":"name is required"}}`, http.StatusBadRequest)
		return
	}
	if _, ok := config.AllowedLLMProviderTypes[req.Type]; !ok {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"unsupported type %q"}}`, req.Type), http.StatusBadRequest)
		return
	}

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	if s.cfg != nil {
		for _, p := range s.cfg.Providers {
			if p.Name == req.Name {
				http.Error(w, `{"error":{"message":"provider name already exists"}}`, http.StatusConflict)
				return
			}
		}
		if s.cfg.RuntimeOverlay != nil {
			for _, p := range s.cfg.RuntimeOverlay.Providers {
				if p.Name == req.Name {
					http.Error(w, `{"error":{"message":"provider name already exists"}}`, http.StatusConflict)
					return
				}
			}
		}
	}

	if s.cfg == nil || s.cfg.RuntimeOverlay == nil {
		http.Error(w, `{"error":{"message":"runtime overlay unavailable"}}`, http.StatusInternalServerError)
		return
	}

	old := append([]config.ProviderConfig(nil), s.cfg.RuntimeOverlay.Providers...)
	s.cfg.RuntimeOverlay.Providers = append(s.cfg.RuntimeOverlay.Providers, config.ProviderConfig{
		Name:    req.Name,
		Type:    req.Type,
		APIBase: req.APIBase,
		APIKey:  req.APIKey,
	})
	if err := config.SaveRuntimeOverlay(s.cfg.RuntimeOverlayPath, s.cfg.RuntimeOverlay); err != nil {
		s.cfg.RuntimeOverlay.Providers = old
		http.Error(w, `{"error":{"message":"save failed"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(adminProvider{
		Name:    req.Name,
		Type:    req.Type,
		APIBase: req.APIBase,
		APIKey:  maskKey(req.APIKey),
	})
}

func (s *Server) handleAdminProviderPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	var req adminProvider
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Type = strings.TrimSpace(req.Type)
	req.APIBase = strings.TrimSpace(req.APIBase)

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	if s.cfg == nil || s.cfg.RuntimeOverlay == nil {
		http.Error(w, `{"error":{"message":"runtime overlay unavailable"}}`, http.StatusInternalServerError)
		return
	}

	idx := -1
	for i, p := range s.cfg.RuntimeOverlay.Providers {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		http.Error(w, `{"error":{"message":"provider not found"}}`, http.StatusNotFound)
		return
	}

	old := s.cfg.RuntimeOverlay.Providers[idx]
	prov := &s.cfg.RuntimeOverlay.Providers[idx]
	prov.Name = req.Name
	prov.Type = req.Type
	prov.APIBase = req.APIBase
	if strings.TrimSpace(req.APIKey) != "" {
		prov.APIKey = strings.TrimSpace(req.APIKey)
	}
	if err := config.SaveRuntimeOverlay(s.cfg.RuntimeOverlayPath, s.cfg.RuntimeOverlay); err != nil {
		s.cfg.RuntimeOverlay.Providers[idx] = old
		http.Error(w, `{"error":{"message":"save failed"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(adminProvider{
		Name:    prov.Name,
		Type:    prov.Type,
		APIBase: prov.APIBase,
		APIKey:  maskKey(prov.APIKey),
	})
}

func (s *Server) handleAdminProviderDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))

	s.adminMu.Lock()
	defer s.adminMu.Unlock()

	if s.cfg == nil || s.cfg.RuntimeOverlay == nil {
		http.Error(w, `{"error":{"message":"runtime overlay unavailable"}}`, http.StatusInternalServerError)
		return
	}

	found := false
	for _, p := range s.cfg.RuntimeOverlay.Providers {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, `{"error":{"message":"provider not found"}}`, http.StatusNotFound)
		return
	}

	oldProviders := append([]config.ProviderConfig(nil), s.cfg.RuntimeOverlay.Providers...)
	oldModels := append([]config.ModelEntry(nil), s.cfg.RuntimeOverlay.Models...)

	providers := make([]config.ProviderConfig, 0, len(s.cfg.RuntimeOverlay.Providers))
	for _, p := range s.cfg.RuntimeOverlay.Providers {
		if p.Name != name {
			providers = append(providers, p)
		}
	}
	s.cfg.RuntimeOverlay.Providers = providers

	models := make([]config.ModelEntry, 0, len(s.cfg.RuntimeOverlay.Models))
	for _, m := range s.cfg.RuntimeOverlay.Models {
		if m.ProviderName() != name {
			models = append(models, m)
		}
	}
	s.cfg.RuntimeOverlay.Models = models

	if err := config.SaveRuntimeOverlay(s.cfg.RuntimeOverlayPath, s.cfg.RuntimeOverlay); err != nil {
		s.cfg.RuntimeOverlay.Providers = oldProviders
		s.cfg.RuntimeOverlay.Models = oldModels
		http.Error(w, `{"error":{"message":"save failed"}}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
