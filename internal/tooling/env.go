package tooling

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

// Env provides environmental context to tool execution.
type Env struct {
	// CWD is the session working directory.
	CWD string

	// PermissionMode controls when the agent requests user approval before running a tool.
	// Values mirror config.PermMode* constants: "ask", "accept_edits", "bypass".
	PermissionMode string

	// CommandAllowlist contains command prefixes/exact commands that never
	// require permission. Checked via CommandAllowed().
	CommandAllowlist []string

	// HTTPAllowlist is tools.http_request.allowlist: destinations an
	// http_request call reaches without a permission prompt.
	HTTPAllowlist []string

	// SessionID is the current session identifier (used by plan tools).
	SessionID string

	// SessionDir is the persisted session bundle (<sessionsRoot>/<id>/) when disk persistence is on.
	SessionDir string

	// Background is the process-wide pool that owns detached commands. Optional;
	// tools must nil-check before use.
	Background *bgtask.Pool

	// BackgroundEnabled reports whether the operator allows detached execution
	// (tools.background.enable).
	BackgroundEnabled bool

	// ArchiveActiveMarkdown moves todos/active.md to todos/archive before starting a replacement list.
	// Optional; wired by the runner when persistence is enabled.
	ArchiveActiveMarkdown func() error

	// WriteArchivedPlanMarkdown persists finalized markdown to todos/archive/plan_<unix>.md when SessionDir is set.
	// Optional; returns the written filesystem path when successful.
	WriteArchivedPlanMarkdown func(markdown string) (pathWritten string, err error)

	// Sender allows tools to send session updates (e.g. PlanUpdate).
	// May be nil - tools must nil-check before use.
	Sender acp.UpdateSender

	// GetPlan returns the current plan entries from session state.
	// May be nil if plan support is not wired up.
	GetPlan func() []acp.PlanEntry

	// SetPlan replaces the plan entries in session state.
	// May be nil if plan support is not wired up.
	SetPlan func([]acp.PlanEntry)

	// ToolCallID is the active LLM tool call id for this execution, when applicable.
	ToolCallID string

	// SSHConnectTimeout is the TCP dial timeout for SSH connections in seconds.
	SSHConnectTimeout int

	// SetSessionMode switches the session operating mode (e.g. plan to agent). Optional.
	SetSessionMode func(mode string) error

	// PersistPlanDocument appends a plan_document transcript row after plan_write. Optional.
	PersistPlanDocument func(doc plans.Document)

	// SendDesignPlanUpdate publishes a design plan preview via session/update plan. Optional.
	SendDesignPlanUpdate func(doc plans.Document)

	// LoadSkillBody returns a loaded skill's full instruction body by its command
	// name, plus the list of available command names, backing the model-driven
	// load_skill tool. Optional; nil when skills auto-discovery is disabled.
	LoadSkillBody func(name string) (body string, available []string, found bool)

	// ConfigPath is the active Coddy YAML file exposed to the config_* tool family.
	ConfigPath string

	// ConfigHome and ConfigCWD preserve the path-expansion context used to load ConfigPath.
	ConfigHome string
	ConfigCWD  string

	// ReloadConfig applies ConfigPath to the live process and current session.
	// config_commit and config_rollback refuse to write when this hook is unavailable.
	ReloadConfig func(ctx context.Context) (warnings []string, err error)

	// ConfigReloaded is set after a successful config_commit or config_rollback
	// so the ReAct loop can refresh definitions before the next model call in
	// the same user turn.
	ConfigReloaded bool

	// SpawnAgent runs a subagent for the spawn_agent tool. Wired by the agent
	// runtime; nil when subagents are unavailable (scheduled runs, disabled).
	SpawnAgent func(ctx context.Context, req SpawnRequest) (string, error)

	// CompactSession folds the older history into a summary for the
	// compact_context tool, the same work /compact does. Wired by the agent
	// runtime; nil when compaction is unavailable for this turn.
	CompactSession func(ctx context.Context, req CompactRequest) (string, error)

	// ContextCompacted is set after a successful compact_context call so the
	// ReAct loop rebuilds its outgoing message slice from the shortened
	// transcript before the next model call.
	ContextCompacted bool

	// FileSession reads and writes how the session is filed - the title it is
	// listed under and the tags it is grouped by. An update that names nothing
	// is a read, which is why there is one hook and not two: the tool never
	// holds a filing it read a moment ago, so it cannot write one that another
	// surface has already moved. Wired by the agent runtime; nil where no
	// session backs the run, and session_describe refuses the call rather than
	// pretending it filed something.
	FileSession func(SessionFilingUpdate) (SessionFilingResult, error)

	// SwitchModel changes the model and/or the reasoning level the session's
	// model requests use, for the switch_model tool. Wired by the agent
	// runtime; nil for a subagent, whose model its parent chose.
	SwitchModel func(ctx context.Context, req ModelSwitch) (string, error)

	// SubagentDepth is how deep this session sits in a spawn tree: 0 for an
	// ordinary session, 1 for its children. The runtime uses it to refuse
	// spawns past subagents.max_depth.
	SubagentDepth int

	// WakeableSession reports whether a finished background task may start a
	// turn on this session (notify_on_finish). False for every session whose
	// transcript is sealed once its one turn returns - a spawned child and a
	// scheduled run alike - so a task started there never registers a wake
	// that the read-only guard would only refuse. A scheduled run sits at
	// depth 0 and still cannot be woken, which is why this is not the depth.
	WakeableSession bool

	// WebSearch is the resolved tools.websearch section the websearch tool
	// reads its engine list and bounds from. It travels on the environment
	// rather than being captured when the registry is built, so a config
	// reload reaches the next search without rebuilding the tool set. Nil
	// means the built-in defaults.
	WebSearch *WebSearchSettings

	// OutputLineLimits caps how many lines each tool result or error may
	// contribute to the LLM context, keyed by tool name; the empty-string key
	// carries the default applied to unlisted (and MCP) tools. A positive value
	// also activates the hard byte ceiling. Nil or 0 disables both limits.
	OutputLineLimits map[string]int
}

// OutputLineLimit returns the effective line ceiling for a tool: its own entry
// when present, otherwise the default (empty-string) entry. 0 disables all
// output limiting for that tool.
func (e *Env) OutputLineLimit(tool string) int {
	if e == nil || e.OutputLineLimits == nil {
		return 0
	}
	if v, ok := e.OutputLineLimits[tool]; ok {
		return v
	}
	return e.OutputLineLimits[""]
}

// CommandAllowed returns true if the given shell command matches an entry
// in the allowlist, meaning it can run without user permission.
//
// Matching rules (case-sensitive).
// Exact match ("make") matches exactly "make" but not "make build".
// Prefix match ("go test ") matches "go test ./..." via allowed entry "go test".
//
// A trailing space is implicitly added to prefix entries to prevent
// "go" from matching "golang-migrate".
func (e *Env) CommandAllowed(command string) bool {
	cmd := strings.TrimSpace(command)
	for _, allowed := range e.CommandAllowlist {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == "*" {
			return true
		}
		if cmd == allowed {
			return true
		}
		if strings.HasPrefix(cmd, allowed+" ") {
			return true
		}
	}
	return false
}

// SpawnRequest is what the spawn_agent tool asks the runtime to run.
type SpawnRequest struct {
	// Agent is the definition name.
	Agent string
	// Model and Reasoning pick the child's model and reasoning level; empty
	// follows the definition, then the parent's model.
	Model     string
	Reasoning string
	// Prompt is the child's task, self-contained.
	Prompt string
	// Description is a short label (3 to 5 words) for the task row and the
	// child session title.
	Description string
	// Background detaches the run and returns the task id at once.
	Background bool
	// ExpectedSeconds, TimeoutSeconds and NotifyOnFinish carry the same
	// meaning as for a background run_command.
	ExpectedSeconds int
	TimeoutSeconds  int
	NotifyOnFinish  bool
}

// CompactRequest is one compact_context call.
type CompactRequest struct {
	// Instructions is optional guidance for the summary.
	Instructions string
	// Model names the summarizer for this one compaction; empty follows the
	// configuration (compaction.model, else the session's model).
	Model string
}

// ModelSwitch is one switch_model call: a model, a reasoning level, or both,
// for the rest of the turn or for the session.
type ModelSwitch struct {
	Model     string
	Reasoning string
	// Session keeps the change for the conversation, as the operator's /model
	// would; false lasts until the current turn ends.
	Session bool
}

// SessionFiling is how one conversation is filed: the title it is listed under
// (the pinned one, or the one derived from the first message) and the tags it
// is grouped by. It is what session_describe reads and reports.
type SessionFiling struct {
	Title string
	Tags  []string
}

// SessionFilingUpdate names the parts of the filing a call changes. A nil field
// is left alone: that is what lets a call add a label without touching a title
// the operator pinned by hand. Tags replaces the whole set, AddTags and
// RemoveTags change it in place, and the two ways are never combined. An update
// naming nothing at all reads the filing without writing it.
type SessionFilingUpdate struct {
	Title      *string
	Tags       *[]string
	AddTags    []string
	RemoveTags []string
}

// SessionFilingResult is what a write answers with: the filing the session
// carries afterwards, and which of its two parts this call actually moved.
// Changed comes from the writes themselves rather than from comparing a filing
// read before and after, so a pin cleared behind a derived title of the same
// words is still reported as a change.
type SessionFilingResult struct {
	Filing  SessionFiling
	Changed []string
}

// WebSearchSettings is the resolved tools.websearch section as the search tool
// receives it. The field order is part of the contract: internal/tools/web
// converts this value to its own Settings type directly, which keeps the
// engine logic in the package that owns it without importing config here.
type WebSearchSettings struct {
	// Engines is the backends to ask, in merge order; empty means the default set.
	Engines []string
	// EngineTimeoutSeconds bounds one backend, TotalTimeoutSeconds the whole call.
	EngineTimeoutSeconds int
	TotalTimeoutSeconds  int
	// MaxConcurrentEngines caps the fan-out when many engines are configured.
	MaxConcurrentEngines int
	// SnippetChars caps one result's description.
	SnippetChars int
	// CacheTTLSeconds is how long one engine's answer is reused; negative
	// turns caching off.
	CacheTTLSeconds int
	// SearXNGURL is an operator's own SearXNG instance, asked over its JSON API.
	SearXNGURL string
	// BraveAPIKey routes the Brave backend to the official Search API.
	BraveAPIKey string
}
