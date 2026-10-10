package session

// ToolKind maps a tool name to the ACP tool call kind a client draws the call
// with: "read" for the tools that only look, "write" for the ones that change
// the workspace or the configuration, "run_command" for the shell and "other"
// for everything else, MCP tools included.
//
// It is the one mapping of the live turn (internal/agent announces each call
// with it) and of the replay of a loaded session (replayConversation), so a
// call shows the same in both. A tool that only looks belongs in the "read"
// case: a name left out is announced as "other" live and on replay alike.
func ToolKind(name string) string {
	switch name {
	case "read", "keep_result", "glob", "grep", "print_tree", "websearch", "webfetch", "config_get", "config_changes", "coddy_docs_search", "coddy_docs_read":
		return "read"
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv", "config_commit", "config_rollback":
		return "write"
	case "run_command":
		return "run_command"
	default:
		return "other"
	}
}
