package tooling

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// Env provides environmental context to tool execution.
type Env struct {
	// CWD is the session working directory.
	CWD string

	// RestrictToCWD prevents operations outside the working directory.
	RestrictToCWD bool

	// RequirePermissionForCommands enables permission prompts for commands.
	RequirePermissionForCommands bool

	// RequirePermissionForWrites enables permission prompts for writes.
	RequirePermissionForWrites bool

	// CommandAllowlist contains command prefixes/exact commands that never
	// require permission. Checked via CommandAllowed().
	CommandAllowlist []string

	// SessionID is the current session identifier (used by plan tools).
	SessionID string

	// Sender allows tools to send session updates (e.g. PlanUpdate).
	// May be nil - tools must nil-check before use.
	Sender acp.UpdateSender

	// GetPlan returns the current plan entries from session state.
	// May be nil if plan support is not wired up.
	GetPlan func() []acp.PlanEntry

	// SetPlan replaces the plan entries in session state.
	// May be nil if plan support is not wired up.
	SetPlan func([]acp.PlanEntry)
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
