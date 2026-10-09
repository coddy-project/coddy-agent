//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// backend is the session surface the console runs against: the in-process
// *session.Manager, or *remote.Handler when --remote points the console at a
// remote coddy serve server. Both expose the same handler methods, so every
// console feature works identically in either mode; remote-specific
// degradations are encoded in the nil returns (SessionByID, FileStore).
type backend interface {
	SetPreferredSessionID(id string)
	// SetNextSessionOrigin marks the session the next HandleSessionNew
	// creates (one-shot print mode marks its runs "print"); reopening a stored
	// session spends the mark without applying it.
	SetNextSessionOrigin(origin string)
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
	// SessionSettings is the settings snapshot of a session: what the footer
	// shows on entering it, its turn overrides included (over --remote the
	// server's last snapshot the client adopted).
	SessionSettings(sessionID string) (acp.SessionSettings, error)
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

// goalBackend is the part of the backend behind the session goal (goal.go):
// what the footer shows on entering a session and what the goal menu reads
// and changes. Every call answers with the goal as it stands afterwards,
// versioned like the session_goal updates, so the console orders the answer
// against the updates that report the same change. In-process it is the
// manager (managerGoals); over --remote, *remote.Handler drives the server's
// GET, PATCH and DELETE /coddy/sessions/{id}/goal.
type goalBackend interface {
	// SessionGoal is the goal the backend holds for a session, without a
	// round trip: the manager's own, or over --remote the last snapshot the
	// client adopted (a loaded session's /messages, an update, an answer).
	SessionGoal(sessionID string) (acp.SessionGoalUpdate, error)
	// FetchSessionGoal reads the goal afresh.
	FetchSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error)
	// PauseSessionGoal stops the automatic continuations and keeps the goal.
	PauseSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error)
	// ClearSessionGoal removes the goal.
	ClearSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error)
}

// managerGoals is the in-process goalBackend: the manager's goal methods,
// each change answered with the snapshot after it. The change itself reaches
// the console as a session_goal update through the manager's sender too,
// with its notice.
type managerGoals struct{ m *session.Manager }

func (g managerGoals) SessionGoal(sessionID string) (acp.SessionGoalUpdate, error) {
	return g.m.SessionGoal(sessionID)
}

func (g managerGoals) FetchSessionGoal(_ context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	return g.m.SessionGoal(sessionID)
}

func (g managerGoals) PauseSessionGoal(_ context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	if _, err := g.m.PauseGoal(sessionID); err != nil {
		return acp.SessionGoalUpdate{}, err
	}
	return g.m.SessionGoal(sessionID)
}

func (g managerGoals) ClearSessionGoal(_ context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	if err := g.m.ClearGoal(sessionID); err != nil {
		return acp.SessionGoalUpdate{}, err
	}
	return g.m.SessionGoal(sessionID)
}

var (
	_ goalBackend = managerGoals{}
	_ goalBackend = (*remote.Handler)(nil)
)
