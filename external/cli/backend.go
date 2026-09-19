//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// backend is the session surface the console runs against: the in-process
// *session.Manager, or *remote.Handler when --remote points the console at a
// remote coddy serve server. Both expose the same handler methods, so every
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
	// ApplySessionSettings changes the session's model, reasoning, mode or
	// permission mode, for the session or for a number of turns: the settings
	// commands and the /permissions picker (session/settings.go; over
	// --remote the server's PATCH /coddy/sessions/{id}).
	ApplySessionSettings(ctx context.Context, sessionID string, ch session.SettingsChange) (acp.SessionSettings, error)
	// Message queue of the running turn: what the operator wrote while the
	// agent works, read at the turn's next step (session/turn_queue.go).
	EnqueueTurnMessage(sessionID, text string) (session.QueuedMessage, []session.QueuedMessage, error)
	// EnqueueFollowUp queues what the operator typed; settings commands at
	// its start apply at once and only the rest is queued.
	EnqueueFollowUp(ctx context.Context, sessionID, text, source string) (session.QueuedMessage, bool, string, error)
	QueuedTurnMessages(sessionID string) ([]session.QueuedMessage, error)
	CancelQueuedTurnMessage(sessionID, messageID string) ([]session.QueuedMessage, error)
	ClearQueuedTurnMessages(sessionID string) error
	// ProviderUsageForSession reads the account usage behind a provider row
	// (the status bar's third line); refresh asks for a fresh read, and a
	// read the backend defers reports its result to sessionID through the
	// sender when it lands. A provider type without a usage source answers
	// Unsupported.
	ProviderUsageForSession(ctx context.Context, sessionID, name string, refresh bool) (*acp.ProviderUsageUpdate, error)
	// The background tasks of a session: what the status line counts and what the
	// /tasks overlay lists, reads and stops (tasks.go). In-process they come from the
	// task pool and the session bundle, over --remote from the server's REST routes;
	// an unknown task is bgtask.ErrNotFound in both.
	BackgroundTasks(ctx context.Context, sessionID string) ([]bgtask.Snapshot, error)
	BackgroundTaskOutput(ctx context.Context, sessionID, taskID string, tailLines int) (string, bgtask.Snapshot, error)
	StopBackgroundTask(ctx context.Context, sessionID, taskID string) (bgtask.Snapshot, error)
	// SearchMentions answers the "@" picker: in-process the manager's search
	// over the session's workspace, over --remote the server's
	// GET /coddy/mentions, so the candidates are where the session runs.
	SearchMentions(ctx context.Context, req session.MentionSearch) (session.MentionSearchResult, error)
}

// Interface conformance is pinned where the concrete types are visible:
// *session.Manager in buildApp, *remote.Handler in buildRemoteApp.
var _ backend = (*session.Manager)(nil)
