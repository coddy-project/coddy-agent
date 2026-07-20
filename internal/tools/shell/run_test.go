package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestRunCommandToolDescriptionMatchesShell(t *testing.T) {
	tests := []struct {
		shell platform.Shell
		want  []string
	}{
		{platform.Shell{Kind: platform.ShellPwsh, Path: "pwsh"}, []string{"PowerShell", "Get-ChildItem", "Select-String", "Get-Process"}},
		{platform.Shell{Kind: platform.ShellCmd, Path: "cmd.exe"}, []string{"cmd.exe", "findstr", "tasklist"}},
		{platform.Shell{Kind: platform.ShellBash, Path: "/bin/bash"}, []string{"bash", "POSIX"}},
	}
	for _, tc := range tests {
		description := RunCommandToolForShell(tc.shell).Definition.Description
		for _, want := range tc.want {
			if !strings.Contains(description, want) {
				t.Fatalf("description %q does not contain %q", description, want)
			}
		}
	}
}

func TestExecuteRunCommandWithCurrentShell(t *testing.T) {
	commandShell := platform.CurrentShell()
	command := "printf coddy-shell-ok"
	switch commandShell.Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		command = "Write-Output 'coddy-shell-ok'"
	case platform.ShellCmd:
		command = "echo coddy-shell-ok"
	}
	args, err := json.Marshal(runCommandArgs{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeRunCommandWithShell(context.Background(), string(args), &tooling.Env{CWD: t.TempDir()}, commandShell)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "coddy-shell-ok") {
		t.Fatalf("output = %q", out)
	}
}
