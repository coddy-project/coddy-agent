//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (s *Server) registerProvidersRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/models", s.coddyProviderModelsGet)
	s.mux.HandleFunc("POST /coddy/providers/models", s.coddyProviderModelsPost)
	s.registerCodexAuthRoutes()
	s.registerNeuralDeepAuthRoutes()
	s.registerProviderUsageRoutes()
}

// coddyProviderModelsGet fetches the model list advertised by a configured
// provider's server. The provider is resolved from the active config by name, so
// its credentials (api_key / api_key_command / NAME_API_KEY env) and proxy apply
// without sending secrets over the wire. On a successful upstream call it returns
// {"ok":true,"models":[{"id","name"}]}; on failure it returns
// {"ok":false,"error":...,"models":[]} with HTTP 200 so the UI can fall back to
// manual model entry. An unknown provider name returns 404.
func (s *Server) coddyProviderModelsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	name := r.PathValue("name")
	var prov *config.ProviderConfig
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			prov = &c.Providers[i]
			break
		}
	}
	if prov == nil {
		writeCoddyConfigErr(w, http.StatusNotFound, "unknown provider")
		return
	}
	s.writeProviderModels(w, r.Context(), c, prov)
}

// providerModelsRequest is the body of POST /coddy/providers/models: a provider
// row as the settings form edits it. The entry being edited has not necessarily
// been saved yet - the sign-in endpoints accept such providers for the same
// reason - so the row travels in the body rather than being looked up in the
// saved config.
type providerModelsRequest struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	APIBase       string `json:"api_base"`
	APIKey        string `json:"api_key"`
	APIKeyCommand string `json:"api_key_command"`
	Proxy         string `json:"proxy"`
}

// coddyProviderModelsPost fetches the model list for a provider description
// posted in the request body. Fields the body leaves empty are inherited from
// the saved provider of the same name when there is one, so a sparse
// {"name": "..."} post resolves stored credentials without secrets travelling
// over the wire; fields the body carries override the saved row, so the form
// previews the provider as it is being edited. Responses follow the GET shape:
// {"ok":true,"models":[...]} on success, {"ok":false,"error":...} with HTTP 200
// on an upstream failure, 400 for a malformed or invalid body.
func (s *Server) coddyProviderModelsPost(w http.ResponseWriter, r *http.Request) {
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	var req providerModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	prov := config.ProviderConfig{
		Name:          req.Name,
		Type:          req.Type,
		APIBase:       req.APIBase,
		APIKey:        req.APIKey,
		APIKeyCommand: req.APIKeyCommand,
		Proxy:         req.Proxy,
	}
	if saved := c.FindProvider(strings.TrimSpace(prov.Name)); saved != nil {
		if strings.TrimSpace(prov.Type) == "" {
			prov.Type = saved.Type
		}
		if strings.TrimSpace(prov.APIBase) == "" {
			prov.APIBase = saved.APIBase
		}
		if strings.TrimSpace(prov.APIKey) == "" && strings.TrimSpace(prov.APIKeyCommand) == "" {
			prov.APIKey = saved.APIKey
			prov.APIKeyCommand = saved.APIKeyCommand
		}
		if strings.TrimSpace(prov.Proxy) == "" {
			prov.Proxy = saved.Proxy
		}
	}
	prov.Normalize()
	if err := prov.Validate(); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeProviderModels(w, r.Context(), c, &prov)
}

// writeProviderModels lists the models a provider advertises and writes the
// {"ok","models"} answer shared by the GET and POST routes.
func (s *Server) writeProviderModels(w http.ResponseWriter, ctx context.Context, c *config.Config, prov *config.ProviderConfig) {
	models, err := llm.ListModels(ctx, llm.ProviderInput{
		Name:     prov.Name,
		Type:     prov.Type,
		APIKey:   prov.EffectiveAPIKey(),
		BaseURL:  prov.APIBase,
		ProxyURL: prov.Proxy,
		AuthPath: config.ProviderAuthPath(c.Paths.Home, prov.Name, prov.Type),
	})
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     false,
			"error":  err.Error(),
			"models": []llm.ModelEntry{},
		})
		return
	}
	if models == nil {
		models = []llm.ModelEntry{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"models": models,
	})
}
