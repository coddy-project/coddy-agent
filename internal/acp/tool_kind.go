package acp

import "strings"

// The ToolKind values of the ACP schema. A client validating updates against
// the schema rejects a tool_call (or a permission request) whose kind is
// anything else, and the call never shows up in the editor.
const (
	ToolKindRead       = "read"
	ToolKindEdit       = "edit"
	ToolKindDelete     = "delete"
	ToolKindMove       = "move"
	ToolKindSearch     = "search"
	ToolKindExecute    = "execute"
	ToolKindThink      = "think"
	ToolKindFetch      = "fetch"
	ToolKindSwitchMode = "switch_mode"
	ToolKindOther      = "other"
)

// IsToolKind reports whether kind is a value of the ACP ToolKind enum.
func IsToolKind(kind string) bool {
	switch kind {
	case ToolKindRead, ToolKindEdit, ToolKindDelete, ToolKindMove, ToolKindSearch,
		ToolKindExecute, ToolKindThink, ToolKindFetch, ToolKindSwitchMode, ToolKindOther:
		return true
	}
	return false
}

// toolKindByName is the ACP kind of the built-in tools whose kind is more
// precise than the agent's internal one ("write" covers a delete and a move,
// "read" covers a search and a fetch).
var toolKindByName = map[string]string{
	"read":              ToolKindRead,
	"write":             ToolKindEdit,
	"edit":              ToolKindEdit,
	"apply_patch":       ToolKindEdit,
	"mkdir":             ToolKindEdit,
	"touch":             ToolKindEdit,
	"config_commit":     ToolKindEdit,
	"config_rollback":   ToolKindEdit,
	"rm":                ToolKindDelete,
	"rmdir":             ToolKindDelete,
	"mv":                ToolKindMove,
	"run_command":       ToolKindExecute,
	"ssh_run_command":   ToolKindExecute,
	"glob":              ToolKindSearch,
	"grep":              ToolKindSearch,
	"coddy_docs_search": ToolKindSearch,
	"webfetch":          ToolKindFetch,
	"websearch":         ToolKindFetch,
	"http_request":      ToolKindFetch,
}

// toolKindByInternal translates the kinds the agent records for its own
// surfaces (the web UI, the console, tool_calls/<id>/meta.json) that the ACP
// schema does not have.
var toolKindByInternal = map[string]string{
	"write":       ToolKindEdit,
	"run_command": ToolKindExecute,
}

// WireToolKind is the kind an ACP client is sent for a tool call the agent
// tagged with kind and titled title. The agent titles a call with the tool
// name ("Run: <name>" for a permission prompt), which picks the most precise
// kind for a built-in tool; otherwise the internal kind is translated, and
// anything the schema does not know becomes "other". The result is always a
// value of the ToolKind enum.
func WireToolKind(kind, title string) string {
	name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(title), "Run:"))
	if k, ok := toolKindByName[name]; ok {
		return k
	}
	if k, ok := toolKindByInternal[kind]; ok {
		return k
	}
	if IsToolKind(kind) {
		return kind
	}
	return ToolKindOther
}

// wireSessionUpdate returns update with any tool kind it carries made valid
// for the ACP schema. The caller's value is never modified: a pointer update
// is copied before its kind is rewritten.
func wireSessionUpdate(update interface{}) interface{} {
	switch u := update.(type) {
	case ToolCallUpdate:
		u.Kind = WireToolKind(u.Kind, u.Title)
		return u
	case *ToolCallUpdate:
		if u == nil {
			return update
		}
		c := *u
		c.Kind = WireToolKind(c.Kind, c.Title)
		return c
	}
	return update
}
