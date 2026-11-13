// Package platform provides the small amount of host-platform detection shared
// by configuration, tools, and prompt construction.
package platform

import (
	"fmt"
	"os/exec"
	"runtime"
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

// InteractiveArgs returns the arguments that start the shell as a session
// reading commands from a pipe, rather than running one command and exiting.
//
// PowerShell prints a banner over the first command unless -NoLogo; the profile
// is deliberately left in place, because this shell belongs to a person and its
// prompt, aliases and functions are what they expect. cmd.exe echoes every line
// it reads from a pipe, which would print each command twice next to the echo
// the caller writes itself: /q turns that off and takes the prompt with it.
func (s Shell) InteractiveArgs() []string {
	switch s.Kind {
	case ShellPwsh, ShellPowerShell:
		return []string{"-NoLogo"}
	case ShellCmd:
		return []string{"/q"}
	default:
		return nil
	}
}

// SessionPrologue returns what is written into a freshly started interactive
// shell before the first command of a person, or an empty string when the shell
// needs nothing.
//
// On Windows it settles the encoding, the way wrapPowerShellCommand does for a
// one-shot command: without it a console program writes in the console output
// code page (866 on a Russian install) and its text reaches the browser as
// mojibake. Bash is asked for its history, which it keeps off for input that is
// not a terminal, so the `history` builtin lists what was typed here.
func (s Shell) SessionPrologue() string {
	switch s.Kind {
	case ShellPwsh, ShellPowerShell:
		return powerShellPrologue
	case ShellCmd:
		return "chcp 65001 >nul 2>&1\r\n"
	case ShellBash:
		return "set -o history\n"
	default:
		return ""
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
