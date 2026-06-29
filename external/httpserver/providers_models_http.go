//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (s *Server) registerProvidersRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/models", s.coddyProviderModelsGet)
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

	models, err := llm.ListModels(r.Context(), llm.ProviderInput{
		Type:     prov.Type,
		APIKey:   prov.EffectiveAPIKey(),
		BaseURL:  prov.APIBase,
		ProxyURL: prov.Proxy,
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
