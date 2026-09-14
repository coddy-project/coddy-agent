//go:build !http

package httpserver

import (
	"context"
	"errors"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
)

// Available reports whether this binary can serve the HTTP API.
const Available = false

// Server is the placeholder the stub hands to OnServer callbacks so a caller
// needs no build tag of its own.
type Server struct{}

// MirrorTurn satisfies session.TurnMirror without publishing anywhere: a build
// with no HTTP API has nothing that could watch a turn.
func (*Server) MirrorTurn(_ string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	return primary, func() {}
}

// RequestDetachedPermission satisfies agent.DetachedPermissionBroker without
// showing anything: a build with no HTTP API has no task row to put a detached
// subagent's prompt on, so nobody can be asked.
func (*Server) RequestDetachedPermission(context.Context, agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	return nil, agent.ErrNoDetachedApprover
}

// Serve reports that the API was left out of this build.
func Serve(context.Context, Options) error {
	return errors.New("httpserver: not built in (rebuild with -tags http)")
}
