package platform

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDetectShellWindowsPriority(t *testing.T) {
	tests := []struct {
		name string
		bins map[string]string
		kind ShellKind
		path string
	}{
		{
			name: "pwsh preferred",
			bins: map[string]string{
				"pwsh":       `C:\Program Files\PowerShell\7\pwsh.exe`,
				"powershell": `C:\Windows\powershell.exe`,
				"cmd":        `C:\Windows\cmd.exe`,
			},
			kind: ShellPwsh,
			path: `C:\Program Files\PowerShell\7\pwsh.exe`,
		},
		{
			name: "Windows PowerShell fallback",
			bins: map[string]string{"powershell": `C:\Windows\powershell.exe`, "cmd": `C:\Windows\cmd.exe`},
			kind: ShellPowerShell,
			path: `C:\Windows\powershell.exe`,
		},
		{
			name: "cmd fallback",
			bins: map[string]string{"cmd": `C:\Windows\cmd.exe`},
			kind: ShellCmd,
			path: `C:\Windows\cmd.exe`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectShell("windows", mapLookup(tc.bins))
			if got.Kind != tc.kind || got.Path != tc.path {
				t.Fatalf("DetectShell() = %+v, want kind=%q path=%q", got, tc.kind, tc.path)
			}
		})
	}
}

func TestDetectShellUnixPriority(t *testing.T) {
	if got := DetectShell("linux", mapLookup(map[string]string{"bash": "/bin/bash", "sh": "/bin/sh"})); got.Kind != ShellBash {
		t.Fatalf("shell = %+v, want bash", got)
	}
	if got := DetectShell("darwin", mapLookup(map[string]string{"sh": "/bin/sh"})); got.Kind != ShellSh {
		t.Fatalf("shell = %+v, want sh", got)
	}
}

func TestShellCommand(t *testing.T) {
	tests := []struct {
		shell   Shell
		command string
		args    []string
	}{
		{Shell{Kind: ShellPwsh, Path: "pwsh"}, "Get-ChildItem", []string{"-NoProfile", "-Command", "Get-ChildItem"}},
		{Shell{Kind: ShellPowerShell, Path: "powershell"}, "Get-Process", []string{"-NoProfile", "-Command", "Get-Process"}},
		{Shell{Kind: ShellCmd, Path: "cmd.exe"}, "dir", []string{"/c", "dir"}},
		{Shell{Kind: ShellBash, Path: "/bin/bash"}, "ls", []string{"-c", "ls"}},
		{Shell{Kind: ShellSh, Path: "/bin/sh"}, "pwd", []string{"-c", "pwd"}},
	}
	for _, tc := range tests {
		executable, args := tc.shell.Command(tc.command)
		if executable != tc.shell.Path || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("Command() = %q %#v, want %q %#v", executable, args, tc.shell.Path, tc.args)
		}
	}
}

func TestEnvironmentPromptContext(t *testing.T) {
	env := Environment{OS: "windows", Arch: "amd64", Shell: Shell{Kind: ShellPwsh, Path: "pwsh"}}
	got := env.PromptContext()
	for _, want := range []string{
		"<environment_context>",
		"<os>windows</os>",
		"<arch>amd64</arch>",
		"<shell>pwsh</shell>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("PromptContext() does not contain %q: %s", want, got)
		}
	}
}

func mapLookup(bins map[string]string) LookupFunc {
	return func(name string) (string, error) {
		if value, ok := bins[name]; ok {
			return value, nil
		}
		return "", errors.New("not found")
	}
}
