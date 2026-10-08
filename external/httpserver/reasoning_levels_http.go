//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// coddyConfigReasoningLevelsGet resolves the reasoning levels a logical model id
// would offer with no reasoning_levels override configured, so the settings form
// can fill the field instead of asking the operator to remember the tiers. The
// model id arrives as a query parameter because the entry being edited is not
// saved yet. The only provider-dependent step is the Codex remap (that backend
// serves gpt-5* ids but calls the lowest tier "none", not "minimal"): the form
// sends the provider type it currently shows as provider_type, which wins over
// the saved config so an unsaved or just-retyped provider row resolves the way
// it will after Save; without the hint the prefix is looked up in the active
// config.
//
// A model id that resolves to no levels is not an error: it is the honest answer
// for a non-reasoning model, reported as {"ok":true,"levels":[],"detected":false}
// so the UI can say so rather than writing an override that hides the selector.
func (s *Server) coddyConfigReasoningLevelsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeCoddyConfigErr(w, http.StatusBadRequest, "model query parameter is required")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// A half-typed id ("valera/" or "qwen3") is normal while the form is open, so
	// report it inline instead of failing the request.
	if _, _, err := config.SplitModelRef(model); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":       false,
			"error":    err.Error(),
			"model":    model,
			"levels":   []string{},
			"detected": false,
		})
		return
	}

	// A nil ReasoningLevels makes the resolver report pure detection, under the
	// same provider-aware remap the composer and GET /v1/models use. The
	// provider's type is the form's hint or, without one, the saved provider's:
	// for the type coddy that answer is none, because a model there has an alias
	// for an id and the remote's listing decides its levels. Reading the
	// configuration's own resolver would hand back the listing, and the form's
	// Fetch would write it into the row, pinning it against the remote.
	entry := &config.ModelEntry{Model: model}
	providerType := strings.TrimSpace(r.URL.Query().Get("provider_type"))
	if providerType == "" {
		if prov := c.FindProvider(entry.ProviderName()); prov != nil {
			providerType = prov.Type
		}
	}
	levels := config.ReasoningLevelsForProviderType(entry, providerType)
	if levels == nil {
		levels = []string{}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"model":    model,
		"levels":   levels,
		"detected": len(levels) > 0,
	})
}
