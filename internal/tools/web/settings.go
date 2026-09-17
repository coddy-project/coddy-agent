package web

import (
	"strings"
	"time"
)

// Default bounds for one websearch call. A blocked engine answers fast (a 202
// or a 403 costs a round trip, not a timeout), so the per-engine budget only
// has to cover a connection that stalls; the total budget is what keeps one
// such connection from holding the turn.
const (
	DefaultEngineTimeoutSeconds = 8
	DefaultTotalTimeoutSeconds  = 20
	DefaultMaxConcurrent        = 4
	// DefaultSnippetChars caps one row's description. "Short snippets" are what
	// the tool promises the model, and a full paragraph per row multiplied by
	// fifteen rows is a measurable share of the context window.
	DefaultSnippetChars = 320
	// DefaultCacheTTLSeconds is how long one engine's answer to one query is
	// reused before the engine is asked again.
	DefaultCacheTTLSeconds = 300
)

// DefaultEngines is what the tool asks when the operator named nothing. Brave
// is first because it is the only keyless backend that answered every probe:
// Bing follows as a second opinion, behind the relevance gate that catches the
// unrelated result sets it serves a client it dislikes. DuckDuckGo and Google
// are not here on purpose - both were measured to return nothing at all from a
// server, so firing them by default buys a round trip and a red line in the
// report. An operator who wants them names them.
func DefaultEngines() []string { return []string{EngineBrave, EngineBing} }

// Settings is the resolved tools.websearch section as the engines see it. It
// travels on the tool environment rather than being captured at registration,
// so a config reload reaches the next search without rebuilding the registry.
type Settings struct {
	// Engines is the backends to ask, in merge order. Empty means DefaultEngines.
	Engines []string
	// EngineTimeoutSeconds bounds one backend; TotalTimeoutSeconds bounds the call.
	EngineTimeoutSeconds int
	TotalTimeoutSeconds  int
	// MaxConcurrentEngines caps the fan-out when many engines are configured.
	MaxConcurrentEngines int
	// SnippetChars caps one row's description.
	SnippetChars int
	// CacheTTLSeconds is how long one engine's answer to one query is reused.
	// A negative value turns the cache off.
	CacheTTLSeconds int
	// SearXNGURL is the base address of an operator's own SearXNG instance,
	// asked through its JSON API. Self-hosted instances usually live on
	// localhost or a LAN address, which is why this is not held to the SSRF
	// rules webfetch applies to a model-supplied URL.
	SearXNGURL string
	// BraveAPIKey routes the Brave backend to the official Search API instead
	// of parsing the HTML page: a stable contract with no parser to break.
	BraveAPIKey string
}

// ResolvedEngines returns the configured engine list, or the default set.
func (s Settings) ResolvedEngines() []string {
	out := make([]string, 0, len(s.Engines))
	seen := make(map[string]bool, len(s.Engines))
	for _, e := range s.Engines {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) == 0 {
		return DefaultEngines()
	}
	return out
}

// EngineTimeout is the per-backend budget.
func (s Settings) EngineTimeout() time.Duration {
	if s.EngineTimeoutSeconds > 0 {
		return time.Duration(s.EngineTimeoutSeconds) * time.Second
	}
	return DefaultEngineTimeoutSeconds * time.Second
}

// TotalTimeout is the budget for the whole call, never below one engine's.
func (s Settings) TotalTimeout() time.Duration {
	d := time.Duration(DefaultTotalTimeoutSeconds) * time.Second
	if s.TotalTimeoutSeconds > 0 {
		d = time.Duration(s.TotalTimeoutSeconds) * time.Second
	}
	if eng := s.EngineTimeout(); d < eng {
		return eng
	}
	return d
}

// MaxConcurrent caps how many backends are in flight at once.
func (s Settings) MaxConcurrent() int {
	if s.MaxConcurrentEngines > 0 {
		return s.MaxConcurrentEngines
	}
	return DefaultMaxConcurrent
}

// SnippetLimit is the per-row description cap.
func (s Settings) SnippetLimit() int {
	if s.SnippetChars > 0 {
		return s.SnippetChars
	}
	return DefaultSnippetChars
}
