package permission

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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
// once per call.
func Options(toolName, argsJSON string) []acp.PermissionOption {
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
