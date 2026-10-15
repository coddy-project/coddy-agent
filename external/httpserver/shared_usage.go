//go:build http

package httpserver

// GET /coddy/llm/models/{alias}/usage (docs/plans/remote-model-provider-phase2.md,
// 4.1): the account usage behind a shared model, as an allowlist projection.
//
// The reading is the manager's own, the one every surface of this host shows
// (Manager.ProviderUsage, from its per-account cache, never forced): any number
// of borrowers, aliases and requests together cost at most one read of the
// upstream account per pacing floor. The projection builds a llm.WireUsage field
// by field. It never copies and deletes from the acp.ProviderUsageUpdate, which
// carries the provider's name and type, the plan, the key's name, the wallet and
// every model id the upstream mentions.

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// sharedUsageWindowID is the ids of the windows that leave the host. It drops
// the "codex:<i>:..." windows of a metered feature, whose label is a feature or
// model name.
var sharedUsageWindowID = regexp.MustCompile(`^(session|week|day|acu|window-[0-9]+)(-secondary)?$`)

// sharedUsageWindowLabel is the labels kept as they are: a duration, the two
// calendar words and the ACU unit. Anything else is replaced by the window's id.
var sharedUsageWindowLabel = regexp.MustCompile(`^([0-9]{1,4}[smhd]|week|day|ACU)$`)

// sharedBlockerModel is the id of the block that concerns the alias's own model.
const sharedBlockerModel = "model_blocked"

// sharedBlockerOther is what a blocker id that is not in the fixed set becomes.
const sharedBlockerOther = "other"

// sharedBlockerIDs is the set of blocker ids a borrower is told about: the ids
// the sources produce, which say why and mean nothing about the lender.
var sharedBlockerIDs = map[string]bool{
	"session_exhausted":        true,
	"week_exhausted":           true,
	"rpm_exhausted":            true,
	"session_cooldown":         true,
	"abuse_cooldown":           true,
	"daily_capacity_exhausted": true,
	"key_blocked":              true,
	"key_cap_blocked":          true,
	"wallet_empty":             true,
	"user_blocked":             true,
	"quota_exhausted":          true,
	sharedBlockerModel:         true,
}

// llmUsageGet answers GET /coddy/llm/models/{alias}/usage: the projection of the
// usage of the account behind the alias, 404 unknown_model for a name that finds
// no shared row (the answer repeats nothing the client sent and names no
// provider), and {"supported":false} for a row whose provider has no usage
// source, whose operator switched the panel off, or a server with no manager.
// It takes no stream slot and never asks the manager for a forced read.
func (s *Server) llmUsageGet(w http.ResponseWriter, r *http.Request) {
	pol := s.authSnapshot(r)
	if pol.anonymousSharedRefused() {
		writeSharedAuthRefusal(w)
		return
	}
	cfg := pol.cfg
	if cfg == nil {
		writeSharedError(w, http.StatusInternalServerError, sharedSetupFailure())
		return
	}
	ent := cfg.FindSharedModel(r.PathValue("alias"))
	if ent == nil {
		// The alias is the only name that finds a row: a provider/model selector,
		// a private model and a missing one answer alike.
		writeSharedError(w, http.StatusNotFound, sharedInvalid(llm.WireCodeUnknownModel, "no shared model is offered under that name"))
		return
	}
	prov := cfg.FindProvider(ent.ProviderName())
	if prov == nil || s.mgr == nil {
		writeSharedUsage(w, llm.WireUsage{})
		return
	}
	// The manager resolves the provider by name against its own active
	// configuration; a request that straddles a reload that retypes the row sees
	// the new row's answer, once. refresh is never true: a holder of a shared-model
	// token cannot make the lender read its account more often than the manager's
	// own pacing allows.
	u, err := s.mgr.ProviderUsage(r.Context(), prov.Name, false)
	if err == nil && u != nil && u.Unsupported {
		writeSharedUsage(w, llm.WireUsage{})
		return
	}
	writeSharedUsage(w, projectSharedUsage(u, err, ent, time.Now()))
}

// writeSharedUsage writes a usage document. An unsupported one is exactly
// {"supported":false} (WireUsage.MarshalJSON).
func writeSharedUsage(w http.ResponseWriter, doc llm.WireUsage) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(doc)
}

// projectSharedUsage is the allowlist projection of one manager reading onto the
// wire document of the alias ent. u and readErr are what Manager.ProviderUsage
// returned; now is the moment of the answer, which ages the delay of a model
// gate (the manager's own windows are aged when it delivers them).
//
// A snapshot that says unsupported is not a document: the zero WireUsage is.
// A read that failed with nothing cached is a supported, stale, empty answer, so
// the lender's credential state is not what a borrower learns.
func projectSharedUsage(u *acp.ProviderUsageUpdate, readErr error, ent *config.ModelEntry, now time.Time) llm.WireUsage {
	if u != nil && u.Unsupported {
		return llm.WireUsage{}
	}
	out := llm.WireUsage{Supported: true, AccountWide: true, Windows: []llm.WireUsageWindow{}}
	if u == nil {
		out.Stale = true
		return out
	}
	out.Stale = readErr != nil || u.Stale || u.Error != ""

	apiModel := ""
	if ent != nil {
		apiModel = strings.ToLower(strings.TrimSpace(ent.APIModel()))
	}
	// An account or a model that bypasses the metered windows spends none of the
	// quota: the windows are empty, and the list that says so never leaves.
	unlimited := u.Unlimited
	for _, m := range u.UnlimitedModels {
		if apiModel != "" && strings.ToLower(strings.TrimSpace(m)) == apiModel {
			unlimited = true
		}
	}
	if !unlimited {
		for _, win := range u.Windows {
			if pw, ok := projectSharedWindow(win); ok {
				out.Windows = append(out.Windows, pw)
			}
		}
	}

	out.Blocked = u.Blocked
	for _, id := range u.Blockers {
		out.Blockers = appendBlocker(out.Blockers, id)
	}
	if u.Blocked && u.RetryInSec > 0 {
		out.RetryInS = u.RetryInSec
	}
	// A block that concerns a model is named under the alias only, and only for
	// the alias's own model: every other model's entry is dropped. The model's own
	// reason and its id stay home; the borrower learns the fixed id and the wait.
	if gate := modelGate(u, apiModel); gate != nil {
		out.Blocked = true
		out.Blockers = appendBlocker(out.Blockers, sharedBlockerModel)
		if wait := gateWait(u, gate, now); wait > out.RetryInS {
			out.RetryInS = wait
		}
	}
	return out
}

// projectSharedWindow projects one window, or reports that it does not leave.
func projectSharedWindow(w acp.UsageWindow) (llm.WireUsageWindow, bool) {
	if !sharedUsageWindowID.MatchString(w.ID) {
		return llm.WireUsageWindow{}, false
	}
	label := w.Label
	if !sharedUsageWindowLabel.MatchString(label) {
		label = w.ID
	}
	out := llm.WireUsageWindow{ID: w.ID, Label: label, UsedPercent: clampUsagePercent(w.UsedPercent), Exhausted: w.Exhausted}
	// The reset is relative to the moment the remote answers, never absolute: the
	// two hosts' clocks differ. A clock anchored by an absolute time that has
	// passed is 0; a relative-only clock that has run out cannot be told from no
	// clock and is left out like it.
	switch {
	case w.ResetsAt != "":
		v := max(w.ResetInSec, 0)
		out.ResetInS = &v
	case w.ResetInSec > 0:
		v := w.ResetInSec
		out.ResetInS = &v
	}
	return out, true
}

// clampUsagePercent keeps a percentage in 0..100 and a NaN out of the encoder.
func clampUsagePercent(v float64) float64 {
	switch {
	case math.IsNaN(v) || v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// appendBlocker adds a blocker id once, an unknown one as "other".
func appendBlocker(list []string, id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return list
	}
	if !sharedBlockerIDs[id] {
		id = sharedBlockerOther
	}
	for _, have := range list {
		if have == id {
			return list
		}
	}
	return append(list, id)
}

// modelGate is the entry of the snapshot that refuses the alias's own model
// (trimmed and case-insensitive, the way the web UI matches it), or nil.
func modelGate(u *acp.ProviderUsageUpdate, apiModel string) *acp.UsageBlockedModel {
	if apiModel == "" {
		return nil
	}
	for i := range u.BlockedModels {
		if strings.ToLower(strings.TrimSpace(u.BlockedModels[i].Model)) == apiModel {
			return &u.BlockedModels[i]
		}
	}
	return nil
}

// gateWait is the seconds until a model gate lifts, at now. The entry's relative
// delay was true when the snapshot was read, so it is aged by the snapshot's age
// (the manager ages the account's own delays, not these); an entry with only an
// absolute time counts from now. A gate that has lifted is 0.
func gateWait(u *acp.ProviderUsageUpdate, gate *acp.UsageBlockedModel, now time.Time) int {
	switch {
	case gate.RetryInSec > 0:
		wait := gate.RetryInSec
		if at, err := time.Parse(time.RFC3339, u.FetchedAt); err == nil {
			if age := int(now.Sub(at).Seconds()); age > 0 {
				wait -= age
			}
		}
		return max(wait, 0)
	case gate.RetryAt != "":
		if at, err := time.Parse(time.RFC3339, gate.RetryAt); err == nil {
			return max(int(at.Sub(now).Seconds()), 0)
		}
	}
	return 0
}
