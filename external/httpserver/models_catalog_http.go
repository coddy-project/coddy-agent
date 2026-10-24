//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/modelcatalog"
)

// modelsCatalogMaxModels bounds the ids one lookup asks about: a provider
// listing of a large aggregator holds a few hundred.
const modelsCatalogMaxModels = 5000

func (s *Server) registerModelsCatalogRoutes() {
	s.mux.HandleFunc("POST /coddy/models/catalog", s.coddyModelsCatalogPost)
}

// modelsCatalogRequest is the body of POST /coddy/models/catalog: a provider
// row as the settings form holds it, and the model ids to look up. No
// credential travels: the catalog is public.
type modelsCatalogRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	APIBase string `json:"api_base"`
	Models  []struct {
		ID string `json:"id"`
		// ContextWindow is the window the provider's own listing reports for
		// the model, 0 when it reports none: the suggested max_tokens is
		// measured against the window requests will use.
		ContextWindow int `json:"context_window"`
	} `json:"models"`
}

// modelsCatalogResponse is the answer: the catalog provider the row matched
// ("" when none), and a suggestion for every id the catalog knows there.
type modelsCatalogResponse struct {
	OK        bool                               `json:"ok"`
	Enabled   bool                               `json:"enabled"`
	Error     string                             `json:"error,omitempty"`
	Provider  string                             `json:"provider,omitempty"`
	FetchedAt *time.Time                         `json:"fetched_at,omitempty"`
	Models    map[string]modelcatalog.Suggestion `json:"models"`
}

// coddyModelsCatalogPost suggests the metadata of a provider row's models from
// the models catalog (models_catalog). Fields the body leaves empty are taken
// from the saved provider of the same name. The answer is HTTP 200 whether or
// not the catalog could be read, so the form falls back to its fields as they
// are: {"ok":true,"enabled":false} while models_catalog.enable is false (no
// request is made), {"ok":false,"error":...} when there is neither a copy nor
// a download. 400 for a malformed body, 415 for one that is not JSON.
func (s *Server) coddyModelsCatalogPost(w http.ResponseWriter, r *http.Request) {
	if !requireJSONRequest(w, r) {
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	var req modelsCatalogRequest
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Models) > modelsCatalogMaxModels {
		writeCoddyConfigErr(w, http.StatusBadRequest, "too many models in one lookup")
		return
	}
	typ, base := strings.TrimSpace(req.Type), strings.TrimSpace(req.APIBase)
	if saved := c.FindProvider(strings.TrimSpace(req.Name)); saved != nil {
		if typ == "" {
			typ = saved.Type
		}
		if base == "" {
			base = saved.APIBase
		}
	}
	out := modelsCatalogResponse{Enabled: c.ModelsCatalog.IsEnabled(), Models: map[string]modelcatalog.Suggestion{}}
	snap, err := s.modelsCatalog.Lookup(r.Context(), c.ModelsCatalog)
	switch {
	case errors.Is(err, modelcatalog.ErrDisabled):
		out.OK = true
		writeJSON(w, http.StatusOK, out)
		return
	case err != nil:
		out.Error = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.OK = true
	out.FetchedAt = &snap.FetchedAt
	out.Provider = snap.Index.ProviderFor(typ, base)
	if out.Provider != "" {
		models := snap.Index[out.Provider].Models
		threshold := c.Compaction.EffectiveThresholdPercent()
		for _, m := range req.Models {
			id := strings.TrimSpace(m.ID)
			if e, ok := models[id]; ok && id != "" {
				out.Models[id] = modelcatalog.Suggest(e, m.ContextWindow, threshold)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
