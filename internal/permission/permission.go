package permission

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// MasterKeyActive reports whether tools.permission_master_key is set (bypasses prompts).
func MasterKeyActive(cfg *config.Config) bool {
	return cfg != nil && strings.TrimSpace(cfg.Tools.PermissionMasterKey) != ""
}

// CommandAllowedWithSession merges config command_allowlist with session-scoped grants (same matching rules).
func CommandAllowedWithSession(env *tooling.Env, sessionCmdGrants []string, cmd string) bool {
	merged := append(append([]string{}, env.CommandAllowlist...), sessionCmdGrants...)
	check := &tooling.Env{CommandAllowlist: merged}
	return check.CommandAllowed(cmd)
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
