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
func (s Shell) Command(command string) (string, []string) {
	switch s.Kind {
	case ShellPwsh, ShellPowerShell:
		return s.Path, []string{"-NoProfile", "-Command", command}
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
