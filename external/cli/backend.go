//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// backend is the session surface the console runs against: the in-process
// *session.Manager, or *remote.Handler when --remote points the console at a
// remote coddy http server. Both expose the same handler methods, so every
// console feature works identically in either mode; remote-specific
// degradations are encoded in the nil returns (SessionByID, FileStore).
type backend interface {
	SetPreferredSessionID(id string)
	ForgetLiveSession(id string)
	// SessionByID returns local session state; nil on a remote backend.
	SessionByID(id string) *session.State
	// FileStore returns local persistence; nil on a remote backend.
	FileStore() *session.FileStore
	// ToolCallResult loads the persisted full tool output (ctrl+o expand).
	ToolCallResult(sessionID, toolCallID string) (string, bool)

	HandleSessionNew(ctx context.Context, params acp.SessionNewParams) (*acp.SessionNewResult, error)
	HandleSessionLoad(ctx context.Context, params acp.SessionLoadParams) (*acp.SessionLoadResult, error)
	HandleSessionList(ctx context.Context, params acp.SessionListParams) (*acp.SessionListResult, error)
	HandleSessionReady(sessionID string)
	HandleSessionCancel(params acp.SessionCancelParams)
	HandleSessionSetMode(ctx context.Context, params acp.SessionSetModeParams) error
	HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
	HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
}

// Interface conformance is pinned where the concrete types are visible:
// *session.Manager in buildApp, *remote.Handler in buildRemoteApp.
var _ backend = (*session.Manager)(nil)
