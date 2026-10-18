package platform

import (
	"reflect"
	"strings"
	"testing"
)

// TestResolveShellDefaults pins the operator shell an empty setting stands for:
// PowerShell on Windows, bash everywhere else, each with the same fallbacks as
// the agent's own shell.
func TestResolveShellDefaults(t *testing.T) {
	tests := []struct {
		name string
		goos string
		bins map[string]string
		kind ShellKind
	}{
		{"windows pwsh", "windows", map[string]string{"pwsh": `C:\pwsh.exe`, "powershell": `C:\ps.exe`, "cmd": `C:\cmd.exe`}, ShellPwsh},
		{"windows powershell", "windows", map[string]string{"powershell": `C:\ps.exe`, "cmd": `C:\cmd.exe`}, ShellPowerShell},
		{"linux bash", "linux", map[string]string{"bash": "/bin/bash", "sh": "/bin/sh", "zsh": "/bin/zsh"}, ShellBash},
		{"darwin bash", "darwin", map[string]string{"bash": "/bin/bash", "zsh": "/bin/zsh"}, ShellBash},
		{"android bash", "android", map[string]string{"bash": "/data/data/com.termux/files/usr/bin/bash"}, ShellBash},
		{"linux without bash", "linux", map[string]string{"sh": "/bin/sh"}, ShellSh},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, spec := range []string{"", "  "} {
				got, err := ResolveShell(spec, tc.goos, mapLookup(tc.bins))
				if err != nil {
					t.Fatalf("ResolveShell(%q) error: %v", spec, err)
				}
				if got.Kind != tc.kind {
					t.Fatalf("ResolveShell(%q) = %+v, want kind %q", spec, got, tc.kind)
				}
			}
		})
	}
}

// TestResolveShellNamedShell covers a shell the operator names: by name on
// PATH or by path, its kind read off the executable's base name.
func TestResolveShellNamedShell(t *testing.T) {
	bins := map[string]string{
		"zsh":                                  "/usr/bin/zsh",
		"fish":                                 "/usr/bin/fish",
		"pwsh":                                 "/usr/local/bin/pwsh",
		"cmd":                                  `C:\Windows\System32\cmd.exe`,
		"powershell.exe":                       `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		"/opt/homebrew/bin/bash":               "/opt/homebrew/bin/bash",
		`C:\Program Files\Git\bin\bash.exe`:    `C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\PowerShell\pwsh.EXE`: `C:\Program Files\PowerShell\pwsh.EXE`,
	}
	tests := []struct {
		spec, goos string
		kind       ShellKind
		path       string
	}{
		{"zsh", "linux", ShellKind("zsh"), "/usr/bin/zsh"},
		{"fish", "darwin", ShellKind("fish"), "/usr/bin/fish"},
		{"pwsh", "linux", ShellPwsh, "/usr/local/bin/pwsh"},
		{"cmd", "windows", ShellCmd, `C:\Windows\System32\cmd.exe`},
		{"powershell.exe", "windows", ShellPowerShell, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{"/opt/homebrew/bin/bash", "darwin", ShellBash, "/opt/homebrew/bin/bash"},
		{`C:\Program Files\Git\bin\bash.exe`, "windows", ShellBash, `C:\Program Files\Git\bin\bash.exe`},
		{`C:\Program Files\PowerShell\pwsh.EXE`, "windows", ShellPwsh, `C:\Program Files\PowerShell\pwsh.EXE`},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := ResolveShell(tc.spec, tc.goos, mapLookup(bins))
			if err != nil {
				t.Fatalf("ResolveShell(%q) error: %v", tc.spec, err)
			}
			if got.Kind != tc.kind || got.Path != tc.path {
				t.Fatalf("ResolveShell(%q) = %+v, want kind %q path %q", tc.spec, got, tc.kind, tc.path)
			}
		})
	}
}

// TestResolveShellUnknownShellIsAnError keeps a typo from falling back to some
// other shell: the operator chose one, and a command meant for it must not run
// in another.
func TestResolveShellUnknownShellIsAnError(t *testing.T) {
	_, err := ResolveShell("nushelll", "linux", mapLookup(map[string]string{"bash": "/bin/bash"}))
	if err == nil || !strings.Contains(err.Error(), "nushelll") {
		t.Fatalf("ResolveShell(unknown) error = %v, want one naming the shell", err)
	}
}

// TestNamedShellCommandUsesDashC: a POSIX-like shell the platform has no kind
// for still takes the command after -c.
func TestNamedShellCommandUsesDashC(t *testing.T) {
	exe, args := Shell{Kind: ShellKind("zsh"), Path: "/bin/zsh"}.Command("echo hi")
	if exe != "/bin/zsh" || !reflect.DeepEqual(args, []string{"-c", "echo hi"}) {
		t.Fatalf("Command() = %q %#v", exe, args)
	}
}
