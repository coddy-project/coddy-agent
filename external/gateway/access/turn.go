//go:build gateway || gateway.telegram || gateway.pachca

package access

import (
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// AdminOnlyNote is what a bot says, and what the agent is told, when somebody
// who is not its admin asks for what only an admin may do.
const AdminOnlyNote = "only the bot's admins can do this"

// nonAdminTools are the only tools a turn of a messenger user who is not the
// bot's admin may call: reading inside the session's working directory, the
// built-in documentation, a web search, the session's own plan and to-do
// list, and a question back. Everything else - the configuration, the model
// (a skill sets one too, so load_skill is out),
// subagents, worktrees, the scheduler, background tasks, servers, MCP tools,
// shell commands, writes, fetching a page from the server - is the admins'.
var nonAdminTools = []string{
	"read", "grep", "glob", "print_tree",
	"coddy_docs_search", "coddy_docs_read", "websearch",
	"question", "plan_read", "plan_list",
	"coddy_todo_plan_read", "coddy_todo_plan_replace", "coddy_todo_plan_archive",
	"coddy_todo_item_add", "coddy_todo_item_remove", "coddy_todo_item_update", "coddy_todo_item_move",
}

// NonAdminTurn is the restriction a bot puts on the turn of a user who is
// not its admin: only the tools above, reads kept inside the working
// directory and out of the agent's home (and so are "@" mentions), and every
// call that needs approval asked about - the bot refuses it - with the
// session's "always allow" grants left aside. An admin's turn runs
// unrestricted.
func NonAdminTurn() *session.TurnRestriction {
	return &session.TurnRestriction{
		AllowedTools:       append([]string(nil), nonAdminTools...),
		AskAlways:          true,
		ConfineToWorkspace: true,
		Note:               AdminOnlyNote,
	}
}

// KeyIsAdmin reports whether the conversation a session key names belongs to
// an admin: a direct chat or an individual group session by its person, a
// group whose isolation is admin by definition, a shared group never (it is
// everybody's).
func KeyIsAdmin(key string, p Policy) bool {
	parts := strings.Split(key, ":")
	switch {
	case len(parts) == 3 && parts[1] == "user":
		return idIsAdmin(parts[2], p)
	case len(parts) == 5 && parts[1] == "chat" && parts[3] == "user":
		return idIsAdmin(parts[4], p)
	case len(parts) == 4 && parts[1] == "chat" && parts[3] == "admin":
		return true
	}
	return false
}

func idIsAdmin(s string, p Policy) bool {
	id, err := strconv.ParseInt(s, 10, 64)
	return err == nil && p.IsAdmin(id)
}

var _ Policy = (*config.TelegramGatewayConfig)(nil)
