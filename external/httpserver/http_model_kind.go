//go:build http

package httpserver

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const ownedByCoddySession = "coddy"

// httpModelListed reports whether sel is accepted as POST /v1 model (profile or configured completion ref).
func httpModelListed(cfg *config.Config, sel string) bool {
	if cfg == nil {
		return false
	}
	switch sel {
	case string(session.ModeAgent), string(session.ModePlan), string(session.ModeAsk):
		return true
	default:
		_, _, err := config.SplitModelRef(sel)
		if err != nil {
			return false
		}
		return cfg.FindModelEntry(sel) != nil
	}
}

// httpModelIsCoddyProfile reports whether sel is agent, plan, or ask (no provider/rest form).
func httpModelIsCoddyProfile(sel string) bool {
	switch sel {
	case string(session.ModeAgent), string(session.ModePlan), string(session.ModeAsk):
		return true
	default:
		return false
	}
}
