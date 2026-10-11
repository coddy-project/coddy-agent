package session_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ToolKind is the one mapping behind both the live turn and the replay of a
// loaded session. It moved here from internal/agent with its cases.
func TestToolKind(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"read", "read"},
		{"keep_result", "read"},
		{"glob", "read"},
		{"grep", "read"},
		{"print_tree", "read"},
		{"websearch", "read"},
		{"webfetch", "read"},
		{"config_get", "read"},
		{"config_changes", "read"},
		{"coddy_docs_search", "read"},
		{"coddy_docs_read", "read"},
		{"write", "write"},
		{"edit", "write"},
		{"apply_patch", "write"},
		{"mkdir", "write"},
		{"rmdir", "write"},
		{"touch", "write"},
		{"rm", "write"},
		{"mv", "write"},
		{"config_commit", "write"},
		{"config_rollback", "write"},
		{"run_command", "run_command"},
		{"compact_context", "other"},
		{"spawn_agent", "other"},
		{"mcp_server__tool", "other"},
		{"", "other"},
	}
	for _, tc := range cases {
		if g := session.ToolKind(tc.name); g != tc.want {
			t.Errorf("ToolKind(%q) = %q, want %q", tc.name, g, tc.want)
		}
	}
}
