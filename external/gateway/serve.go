// Package gateway provides a pluggable messenger gateway for Coddy Agent.
//
// The adapters are behind the gateway build tags. This file is not: it carries
// the options a caller fills in and the flag that says whether this binary has
// any adapter at all, so `coddy serve` can read a configuration that asks for a
// bot and answer honestly in a build that has none.
package gateway

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// PromptSurfaces is where an adapter offers to show the permission prompt of a
// subagent whose parent turn has ended. `coddy serve` passes its runtime, which
// offers each such prompt to every surface of the process at once.
type PromptSurfaces interface {
	AddDetachedPermissionApprover(agent.DetachedPermissionBroker) (withdraw func())
}

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
	// Prompts is where a bot offers to ask about a detached subagent of one of
	// its chats. Nil means such a prompt is never shown in a chat.
	Prompts PromptSurfaces
	// Wakes is where a bot offers to run the turn a finished background task
	// starts in the session one of its chats is bound to, so the answer lands
	// in that chat. Nil means a woken turn never reaches a chat.
	Wakes agent.WakeSurfaces
}
