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

	// SessionID is the current session identifier (used by plan tools).
	SessionID string

	// SessionDir is the persisted session bundle (<sessionsRoot>/<id>/) when disk persistence is on.
	SessionDir string

	// Background is the process-wide pool that owns detached commands. Optional;
	// tools must nil-check before use.
	Background *bgtask.Pool

	// BackgroundEnabled reports whether the operator allows detached execution
	// (tools.background.enabled).
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

	// SubagentDepth is how deep this session sits in a spawn tree: 0 for an
	// ordinary session, 1 for its children. The runtime uses it to refuse
	// spawns past subagents.max_depth.
	SubagentDepth int

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
