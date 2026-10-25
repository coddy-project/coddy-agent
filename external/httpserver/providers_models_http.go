//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

func (s *Server) registerProvidersRoutes() {
	s.mux.HandleFunc("GET /coddy/providers/{name}/models", s.coddyProviderModelsGet)
	s.mux.HandleFunc("POST /coddy/providers/models", s.coddyProviderModelsPost)
	s.registerCodexAuthRoutes()
	s.registerNeuralDeepAuthRoutes()
	s.registerOpenCodeAuthRoutes()
	s.registerProviderUsageRoutes()
}

// coddyProviderModelsGet fetches the model list advertised by a configured
// provider's server. The provider is resolved from the active config by name, so
// its credentials (api_key / api_key_command / NAME_API_KEY env) and proxy apply
// without sending secrets over the wire. On a successful upstream call it returns
// {"ok":true,"models":[{"id","name","context_window"}]}; on failure it returns
// {"ok":false,"error":...,"models":[]} with HTTP 200 so the UI can fall back to
// manual model entry. A model the provider reports no window for and the
// built-in models catalog knows also carries
// {"catalog":{"context_window","max_tokens"}}. An unknown provider name returns
// 404.
func (s *Server) coddyProviderModelsGet(w http.ResponseWriter, r *http.Request) {
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
// previews the provider as it is being edited. The credential pair
// (api_key / api_key_command) is one slot: it is inherited only when the body
// posts neither field, and only while the resolved api_base and proxy still
// match the saved row, so an overridden route never receives stored
// credentials. A
// posted api_key_command is executed server-side, exactly as it would be for a
// saved provider. Only an application/json body is accepted (415 otherwise),
// which keeps a cross-site page from driving this route through a browser.
// Responses follow the GET shape: {"ok":true,"models":[...]} on success,
// {"ok":false,"error":...} with HTTP 200 on an upstream failure, 400 for a
// malformed or invalid body.
func (s *Server) coddyProviderModelsPost(w http.ResponseWriter, r *http.Request) {
	// Only a JSON body is read: an unauthenticated loopback server would
	// otherwise run the posted api_key_command, or send a stored key
	// upstream, for a page on another site (see requireJSONRequest).
	if !requireJSONRequest(w, r) {
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	var req providerModelsRequest
	// A provider row is a few hundred bytes; nothing needs more than this.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	prov := config.ProviderConfig{
		Name:          strings.TrimSpace(req.Name),
		Type:          req.Type,
		APIBase:       req.APIBase,
		APIKey:        req.APIKey,
		APIKeyCommand: req.APIKeyCommand,
		Proxy:         req.Proxy,
	}
	if saved := c.FindProvider(prov.Name); saved != nil {
		if strings.TrimSpace(prov.Type) == "" {
			prov.Type = saved.Type
		}
		if strings.TrimSpace(prov.APIBase) == "" {
			prov.APIBase = saved.APIBase
		}
		if strings.TrimSpace(prov.Proxy) == "" {
			prov.Proxy = saved.Proxy
		}
		// The credential pair is one slot: it is inherited only when the body
		// posts neither field, and only while the request still takes the
		// saved route - the saved endpoint through the saved proxy. A caller
		// overriding either must post the credentials for it, otherwise the
		// stored key would go to a URL, or through a proxy, it was never
		// configured for.
		if strings.TrimSpace(prov.APIBase) == strings.TrimSpace(saved.APIBase) &&
			strings.TrimSpace(prov.Proxy) == strings.TrimSpace(saved.Proxy) &&
			strings.TrimSpace(prov.APIKey) == "" &&
			strings.TrimSpace(prov.APIKeyCommand) == "" {
			prov.APIKey = saved.APIKey
			prov.APIKeyCommand = saved.APIKeyCommand
		}
	}
	if strings.TrimSpace(prov.Type) == "acp" {
		// An acp row runs a program. The request names no program of its
		// own: the agent is the saved row's, so listing models never starts
		// a command that only travelled in a request body.
		saved := c.FindProvider(prov.Name)
		if saved == nil || saved.Type != "acp" || saved.ACP == nil {
			writeCoddyConfigErr(w, http.StatusBadRequest, "save the acp provider first: its agent is started only from the saved configuration")
			return
		}
		// A copy: Normalize writes into the block, and the saved one is
		// what running turns read.
		acpCopy := *saved.ACP
		prov.ACP = &acpCopy
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
		APIKey:   prov.EffectiveAPIKeyContext(ctx),
		BaseURL:  prov.APIBase,
		ProxyURL: prov.Proxy,
		AuthPath: config.ProviderAuthPath(c.Paths.Home, prov.Name, prov.Type),
		// An unsaved row counts toward the rows of its type too, so the
		// preview uses the account a saved row would.
		NoCLILogin: !c.ProviderMayUseCLILogin(prov.Name, prov.Type),
		ACP:        prov.ACP,
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
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"models": withCatalogLimits(models),
	})
}

// providerModel is a listed model as the settings form gets it: what the
// provider reports and, for a model it reports no window for, what the
// built-in models catalog knows of the id, which the form fills a model it adds
// from. The catalog's numbers come as a pair or not at all: its max_tokens was
// worked out for its own window and fits no other.
type providerModel struct {
	llm.ModelEntry
	Catalog *catalogLimits `json:"catalog,omitempty"`
}

type catalogLimits struct {
	ContextWindow int `json:"context_window"`
	MaxTokens     int `json:"max_tokens,omitempty"`
}

func withCatalogLimits(listed []llm.ModelEntry) []providerModel {
	out := make([]providerModel, len(listed))
	for i, m := range listed {
		out[i].ModelEntry = m
		if m.ContextWindow > 0 {
			continue
		}
		if limits, ok := models.Lookup(m.ID); ok {
			out[i].Catalog = &catalogLimits{ContextWindow: limits.ContextWindow, MaxTokens: limits.MaxTokens}
		}
	}
	return out
}
