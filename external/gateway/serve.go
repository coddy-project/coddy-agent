// Package gateway provides a pluggable messenger gateway for Coddy Agent.
//
// The adapters are behind the gateway build tags. This file is not: it carries
// the options a caller fills in and the flag that says whether this binary has
// any adapter at all, so `coddy serve` can read a configuration that asks for a
// bot and answer honestly in a build that has none.
package gateway

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Options are what one gateway hub is built from.
type Options struct {
	// Cfg is the configuration the adapters start with.
	Cfg *config.Config
	// Mgr owns the sessions the chats talk to.
	Mgr *session.Manager
	// Log is the process logger.
	Log *slog.Logger
	// DefaultCWD is the workspace a chat session gets.
	DefaultCWD string
	// Mirror publishes a chat turn where other surfaces in this process can
	// watch it. Nil means nothing is watching.
	Mirror session.TurnMirror
}
