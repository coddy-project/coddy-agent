// Package platform provides the small amount of host-platform detection shared
// by configuration, tools, and prompt construction.
package platform

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ShellKind identifies a supported command interpreter.
type ShellKind string

const (
	ShellPwsh       ShellKind = "pwsh"
	ShellPowerShell ShellKind = "powershell"
	ShellCmd        ShellKind = "cmd"
	ShellBash       ShellKind = "bash"
	ShellSh         ShellKind = "sh"
)

// LookupFunc resolves an executable name to a path.
type LookupFunc func(string) (string, error)

// Shell is a detected command interpreter and its resolved executable path.
type Shell struct {
	Kind ShellKind
	Path string
}

// Command converts a command string into executable and argv values for the shell.
// PowerShell commands are wrapped (see wrapPowerShellCommand) so that output
// encoding and exit codes survive; the command text itself is never rewritten.
func (s Shell) Command(command string) (string, []string) {
	switch s.Kind {
	case ShellPwsh, ShellPowerShell:
		return s.Path, []string{"-NoProfile", "-Command", wrapPowerShellCommand(command)}
	case ShellCmd:
		return s.Path, []string{"/c", command}
	default:
		return s.Path, []string{"-c", command}
	}
}

// DetectShell selects the first available shell for goos.
func DetectShell(goos string, lookup LookupFunc) Shell {
	if lookup == nil {
		lookup = exec.LookPath
	}
	if goos == "windows" {
		for _, candidate := range []struct {
			name string
			kind ShellKind
		}{
			{name: "pwsh", kind: ShellPwsh},
			{name: "powershell", kind: ShellPowerShell},
			{name: "cmd", kind: ShellCmd},
		} {
			if path, err := lookup(candidate.name); err == nil {
				return Shell{Kind: candidate.kind, Path: path}
			}
		}
		return Shell{Kind: ShellCmd, Path: "cmd.exe"}
	}

	for _, candidate := range []struct {
		name string
		kind ShellKind
	}{
		{name: "bash", kind: ShellBash},
		{name: "sh", kind: ShellSh},
	} {
		if path, err := lookup(candidate.name); err == nil {
			return Shell{Kind: candidate.kind, Path: path}
		}
	}
	return Shell{Kind: ShellSh, Path: "sh"}
}

// ResolveShell turns the operator's shell setting into a Shell for goos. An
// empty spec is the default: what DetectShell picks, PowerShell on Windows and
// bash elsewhere. Otherwise spec is a name looked up on PATH (zsh, pwsh, cmd)
// or a path to the executable, and the kind - hence how the command is passed
// - is read off the executable's base name. A spec that does not resolve is an
// error rather than a fallback: a command written for the chosen shell must not
// run in another one.
func ResolveShell(spec, goos string, lookup LookupFunc) (Shell, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return DetectShell(goos, lookup), nil
	}
	if lookup == nil {
		lookup = exec.LookPath
	}
	path, err := lookup(spec)
	if err != nil {
		return Shell{}, fmt.Errorf("shell %q not found: %w", spec, err)
	}
	return Shell{Kind: shellKindOf(spec), Path: path}, nil
}

// shellKindOf names the kind of the shell an executable name or path stands
// for. Both separators count whatever the host is, so a Windows path in a
// config read on another machine still names its shell.
func shellKindOf(spec string) ShellKind {
	base := spec
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	return ShellKind(strings.TrimSuffix(base, ".exe"))
}

// CurrentShell detects the shell for the running process.
func CurrentShell() Shell {
	return DetectShell(runtime.GOOS, exec.LookPath)
}

// Environment describes the host facts exposed to the model.
type Environment struct {
	OS    string
	Arch  string
	Shell Shell
}

// CurrentEnvironment detects the current host environment.
func CurrentEnvironment() Environment {
	return Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, Shell: CurrentShell()}
}

// PromptContext renders the host facts appended to the system prompt.
func (e Environment) PromptContext() string {
	return fmt.Sprintf("<environment_context>\n<os>%s</os>\n<arch>%s</arch>\n<shell>%s</shell>\n</environment_context>", e.OS, e.Arch, e.Shell.Kind)
}
