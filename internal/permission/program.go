package permission

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// OptionAllowAlwaysProgram is the permission option id for widening a grant from
// one exact command to the program it invokes.
const OptionAllowAlwaysProgram = "allow_always_program"

// OptionAllow, OptionAllowAlways and OptionReject are the ids of the choices
// every permission dialog offers; a client answers with one of them.
const (
	OptionAllow       = "allow"
	OptionAllowAlways = "allow_always"
	OptionReject      = "reject"
)

// OutcomeCancelled is the outcome a client reports when the request was
// dismissed instead of answered.
const OutcomeCancelled = "cancelled"

// OptionAllowSessionBypass and OptionAllowSessionAcceptEdits approve the call
// and switch the session's permission mode for the rest of the session: to
// bypass, or to accept_edits for a file write (#292). They are offered only
// while the session asks (mode ask), on the session's own prompts.
const (
	OptionAllowSessionBypass      = "allow_session_bypass"
	OptionAllowSessionAcceptEdits = "allow_session_accept_edits"
)

// SessionModeOption returns the permission mode an answer switches the
// session to, or "" when the answer switches nothing.
func SessionModeOption(res *acp.PermissionResult) string {
	if res == nil {
		return ""
	}
	switch strings.TrimSpace(res.OptionID) {
	case OptionAllowSessionBypass:
		return config.PermModeBypass
	case OptionAllowSessionAcceptEdits:
		return config.PermModeAcceptEdits
	}
	return ""
}

// AutoApproves reports whether a sender answers a permission request itself,
// without asking anybody: the mode of the agent that asks is bypass. A
// subagent's own narrowed mode decides first, then the mode the session's
// gate stamped, then the configuration's, for a request that carries neither
// (a caller outside the agent's gate).
func AutoApproves(params acp.PermissionRequestParams, configMode string) bool {
	if m := strings.TrimSpace(params.EffectivePermissionMode); m != "" {
		return m == config.PermModeBypass
	}
	if m := strings.TrimSpace(params.SessionPermissionMode); m != "" {
		return m == config.PermModeBypass
	}
	return configMode == config.PermModeBypass
}

// shellMetacharacters are the characters that let one command line run more than
// one command, redirect it, or substitute another. A grant is only ever offered
// for a command free of all of them: approving "curl https://example.com" must
// never end up authorising "curl https://example.com | sh".
//
// `%` and `@` are included for the Windows shells: `%VAR%` is cmd.exe expansion
// and `@args` is PowerShell splatting. Neither executes a second command on its
// own, but a grant is a long-lived decision and refusing to widen is the cheap
// side of that trade.
const shellMetacharacters = "|&;<>()$`\n\r*?[]{}!#~%@"

// multiplexers are programs whose first argument selects what actually happens,
// so widening to the bare program name would be far broader than what the
// operator saw. For these the grant keeps the subcommand: approving
// "git status --short" grants "git status", not "git".
var multiplexers = map[string]bool{
	"apt":            true,
	"apt-get":        true,
	"brew":           true,
	"cargo":          true,
	"docker":         true,
	"docker-compose": true,
	"gh":             true,
	"git":            true,
	"go":             true,
	"helm":           true,
	"kubectl":        true,
	"make":           true,
	"npm":            true,
	"npx":            true,
	"pip":            true,
	"pip3":           true,
	"pnpm":           true,
	"poetry":         true,
	"systemctl":      true,
	"terraform":      true,
	"uv":             true,
	"yarn":           true,
}

// ProgramGrant returns the allowlist entry that a program-wide grant would add
// for cmd, and whether widening is safe to offer at all.
//
// The returned entry is exactly what the permission dialog names on its button,
// so the operator approves the string that is actually stored. Widening is
// refused for anything but a single plain invocation: shell metacharacters, a
// leading environment assignment, or an empty command all keep the narrow
// exact-command grant that "Allow always" already provides.
func ProgramGrant(cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	if !isPlainInvocation(cmd) {
		return "", false
	}

	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", false
	}

	program := fields[0]
	if !multiplexers[programBase(program)] {
		return program, true
	}
	if len(fields) < 2 {
		return program, true
	}
	subcommand := fields[1]
	if strings.HasPrefix(subcommand, "-") {
		return program, true
	}
	return program + " " + subcommand, true
}

// Options returns the choices the permission dialog offers for a tool call.
//
// Every tool gets allow / allow always / reject. A shell command that can be
// widened safely gets a fourth choice naming the grant it would store, so a
// batch of calls differing only in their arguments is approved once instead of
// once per call. An http_request names its address and its origin instead of
// a bare "allow always", because those are what the grant would cover.
func Options(toolName, argsJSON string) []acp.PermissionOption {
	return OptionsFor(toolName, argsJSON, OptionContext{})
}

// OptionContext is what the gate knows about a prompt beyond the call itself:
// whether it may offer to switch the session's permission mode.
type OptionContext struct {
	// Mode is the permission mode the session asks under.
	Mode string
	// SessionSwitch allows the session-wide options at all: false for a
	// subagent's prompt (its mode was narrowed by its definition and a child
	// changes nothing of its parent) and for a prompt a hook forced.
	SessionSwitch bool
}

// OptionsFor is Options with the session-wide switches offered where they
// apply: while the session asks (mode ask), "allow edits for this session"
// on a file write and "bypass for this session" on anything but
// the staged-config commit and rollback, which keep asking because a commit
// can rewrite the permission policy itself.
func OptionsFor(toolName, argsJSON string, oc OptionContext) []acp.PermissionOption {
	options := baseOptions(toolName, argsJSON)
	if !oc.SessionSwitch || oc.Mode != config.PermModeAsk {
		return options
	}
	var extra []acp.PermissionOption
	name := strings.TrimSpace(toolName)
	if len(WriteGrantKeys(name, argsJSON, "/")) > 0 {
		extra = append(extra, acp.PermissionOption{OptionID: OptionAllowSessionAcceptEdits, Name: "Allow edits for this session", Kind: "allow_always"})
	}
	if name != "config_commit" && name != "config_rollback" {
		extra = append(extra, acp.PermissionOption{OptionID: OptionAllowSessionBypass, Name: "Bypass for this session", Kind: "allow_always"})
	}
	if len(extra) == 0 {
		return options
	}
	// Before the reject option, which stays last.
	last := options[len(options)-1]
	out := append(append(options[:len(options)-1:len(options)-1], extra...), last)
	return out
}

func baseOptions(toolName, argsJSON string) []acp.PermissionOption {
	if strings.TrimSpace(toolName) == web.ToolHTTPRequest {
		return httpRequestOptions(argsJSON)
	}
	options := []acp.PermissionOption{
		{OptionID: OptionAllow, Name: "Allow", Kind: "allow_once"},
		{OptionID: OptionAllowAlways, Name: "Allow always", Kind: "allow_always"},
	}
	if strings.TrimSpace(toolName) == "run_command" {
		if grant, ok := ProgramGrant(ExtractRunCommand(argsJSON)); ok {
			options = append(options, acp.PermissionOption{
				OptionID: OptionAllowAlwaysProgram,
				Name:     "Always allow " + grant,
				Kind:     "allow_always",
			})
		}
	}
	return append(options, acp.PermissionOption{OptionID: OptionReject, Name: "Reject", Kind: "reject_once"})
}

// isPlainInvocation reports whether cmd is a single command with no shell
// machinery: no pipeline, no sequencing, no redirection, no substitution, no
// glob, and no leading environment assignment.
//
// It gates both ends of a program-wide grant. Checking only the command being
// approved would be useless on its own, because the stored entry is matched as
// a prefix later: the command being matched has to be plain too.
func isPlainInvocation(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if strings.ContainsAny(cmd, shellMetacharacters) {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	program := fields[0]
	// "FOO=bar curl ..." would otherwise be treated as an invocation of FOO=bar.
	if strings.Contains(program, "=") {
		return false
	}
	if strings.HasSuffix(program, "/") || strings.HasSuffix(program, `\`) {
		return false
	}
	return true
}

// programBase strips any directory prefix so that "/usr/bin/git" is recognised
// as the same multiplexer as "git".
func programBase(program string) string {
	if idx := strings.LastIndexAny(program, `/\`); idx >= 0 {
		return program[idx+1:]
	}
	return program
}
