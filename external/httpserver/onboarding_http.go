//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/providerlogin"
)

func (s *Server) registerOnboardingRoutes() {
	s.mux.HandleFunc("GET /coddy/onboarding/status", s.coddyOnboardingStatusGet)
}

type onboardingStatusDTO struct {
	FirstRun            bool     `json:"first_run"`
	HasConfig           bool     `json:"has_config"`
	HasProviders        bool     `json:"has_providers"`
	HasModels           bool     `json:"has_models"`
	HasAgentModel       bool     `json:"has_agent_model"`
	HasAgentCredentials bool     `json:"has_agent_credentials"`
	MissingAPIKeys      []string `json:"missing_api_keys"`
}

func (s *Server) coddyOnboardingStatusGet(w http.ResponseWriter, r *http.Request) {
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	_, statErr := os.Stat(strings.TrimSpace(c.Paths.ConfigPath))
	hasConfig := statErr == nil
	missing := make([]string, 0)
	for _, prov := range c.Providers {
		if !providerlogin.ProviderReady(c, prov.Name) {
			missing = append(missing, prov.Name)
		}
	}
	modelRef := strings.TrimSpace(c.Agent.Model)
	if modelRef == "" {
		modelRef = c.FirstModelID()
	}
	agentReady := false
	if name, _, err := config.SplitModelRef(modelRef); err == nil {
		agentReady = providerlogin.ProviderReady(c, name)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(onboardingStatusDTO{
		FirstRun:            !hasConfig,
		HasConfig:           hasConfig,
		HasProviders:        len(c.Providers) > 0,
		HasModels:           len(c.Models) > 0,
		HasAgentModel:       modelRef != "",
		HasAgentCredentials: agentReady,
		MissingAPIKeys:      missing,
	}); err != nil {
		s.log.Error("onboarding status encode", "error", err)
	}
}
