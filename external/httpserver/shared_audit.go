//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/shareguard"
)

// The credential classes a counter row is labelled with: the class, never the
// credential, and never anything that links a token to an alias.
const (
	sharedClassMain      = "main"
	sharedClassShared    = "shared"
	sharedClassLogin     = "login"
	sharedClassAnonymous = "anonymous"
	// sharedClassUnknown is a bearer the gate refused on a shared route.
	sharedClassUnknown = "unknown"
)

// The outcomes of a counter row: the kinds of a call, with the window refusal
// told apart from a full slot.
const (
	sharedCountOK       = "ok"
	sharedCountBusy     = "busy"
	sharedCountLimited  = "limited"
	sharedCountRate     = "rate"
	sharedCountQuota    = "quota"
	sharedCountUpstream = "upstream"
	sharedCountInvalid  = "invalid"
	sharedCountAuth     = "auth"
	sharedCountGone     = "gone"
	sharedCountWrite    = "write"
)

// sharedStatsPattern serves the counters. It sits outside /coddy/llm/ on
// purpose, so no prefix rule can ever admit it for a shared-model token: it is
// read with a main token or a sign-in, like the rest of the API.
const sharedStatsPattern = "GET /coddy/shared-models/stats"

// sharedClassOf is the credential class of a request that passed the gate: what
// the gate's snapshot accepted, never what the client merely sent.
func (s *Server) sharedClassOf(r *http.Request, pol *authPolicy) string {
	if pol == nil || !pol.enabled {
		return sharedClassAnonymous
	}
	t := bearerToken(r)
	switch {
	case acceptBearer(pol.tokens, t):
		return sharedClassMain
	case acceptBearer(pol.sharedTokens, t):
		return sharedClassShared
	case s.hasCookieSession(r, pol.login):
		return sharedClassLogin
	}
	return sharedClassAnonymous
}

// sharedOutcomeLabel is the outcome of a finished call as a counter row names it.
func sharedOutcomeLabel(call *sharedCallLog) string {
	switch call.kind {
	case sharedOutcomeOK:
		return sharedCountOK
	case llm.WireKindBusy:
		if call.code == llm.WireCodeRateWindow {
			return sharedCountLimited
		}
		return sharedCountBusy
	case llm.WireKindRate:
		return sharedCountRate
	case llm.WireKindQuota:
		return sharedCountQuota
	case llm.WireKindUpstream:
		return sharedCountUpstream
	case llm.WireKindInvalid:
		return sharedCountInvalid
	case llm.WireKindAuth:
		return sharedCountAuth
	case sharedOutcomeGone:
		return sharedCountGone
	case sharedOutcomeWrite:
		return sharedCountWrite
	}
	return sharedCountUpstream
}

// countSharedCall adds a finished call to the counters. alias is "-" when the
// request never named a row of this node, so a probe cannot make a key.
func (s *Server) countSharedCall(call *sharedCallLog, took time.Duration) {
	if s.sharedStats == nil {
		return
	}
	class := call.class
	if class == "" {
		class = sharedClassAnonymous
	}
	s.sharedStats.Add(shareguard.Key{call.alias, class, sharedOutcomeLabel(call)}, shareguard.Delta{
		Calls: 1, InputTokens: int64(call.inputTokens), OutputTokens: int64(call.outputTokens),
		DurationMS: took.Milliseconds(),
	})
}

// countSharedGateRefusal is the count of a bearer the gate refused on a shared
// route: the handler never ran, so the gate counts it, once.
func (s *Server) countSharedGateRefusal() {
	if s.sharedStats == nil {
		return
	}
	s.sharedStats.Add(shareguard.Key{"-", sharedClassUnknown, sharedCountAuth}, shareguard.Delta{Calls: 1})
}

// sharedStatsRow and sharedStatsDoc are the document of the stats route. The
// keys are fixed by a test, and no credential, prompt or upstream name is in it.
type sharedStatsRow struct {
	Alias         string `json:"alias"`
	Class         string `json:"class"`
	Outcome       string `json:"outcome"`
	Calls         int64  `json:"calls"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	DurationMS    int64  `json:"duration_ms"`
	MaxDurationMS int64  `json:"max_duration_ms"`
}

type sharedStatsDoc struct {
	Since string           `json:"since"`
	Rows  []sharedStatsRow `json:"rows"`
}

// sharedStatsGet answers GET /coddy/shared-models/stats.
func (s *Server) sharedStatsGet(w http.ResponseWriter, _ *http.Request) {
	doc := sharedStatsDoc{Rows: []sharedStatsRow{}}
	if s.sharedStats != nil {
		snap := s.sharedStats.Snapshot()
		doc.Since = snap.Since.UTC().Format(time.RFC3339)
		for _, r := range snap.Rows {
			doc.Rows = append(doc.Rows, sharedStatsRow{
				Alias: r.Key[0], Class: r.Key[1], Outcome: r.Key[2], Calls: r.Calls,
				InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
				DurationMS: r.DurationMS, MaxDurationMS: r.MaxDurationMS,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(doc)
}
