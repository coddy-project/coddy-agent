package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestRunCommandToolDescriptionMatchesShell(t *testing.T) {
	tests := []struct {
		shell platform.Shell
		want  []string
	}{
		// The PowerShell description also has to tell the model how to pass literal
		// text: emitting a backtick inside double quotes is what made run_command
		// fail in practice.
		{platform.Shell{Kind: platform.ShellPwsh, Path: "pwsh"}, []string{"PowerShell", "Get-ChildItem", "Select-String", "Get-Process", "here-string", "@'...'@", "backtick", "heredoc"}},
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

func TestRunCommandSchemaOffersBackgroundExecution(t *testing.T) {
	schema, ok := RunCommandToolForShell(platform.Shell{Kind: platform.ShellBash, Path: "/bin/bash"}).Definition.InputSchema.(map[string]interface{})
	if !ok {
		t.Fatal("input schema is not an object")
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("input schema has no properties object: %#v", schema)
	}
	for _, name := range []string{"background", "expected_seconds"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("run_command schema is missing %q", name)
		}
	}
}

func TestBackgroundToolsRefuseWithoutAPool(t *testing.T) {
	tools := map[string]*tooling.Tool{
		ToolBackgroundList:   BackgroundListTool(),
		ToolBackgroundOutput: BackgroundOutputTool(),
		ToolBackgroundWait:   BackgroundWaitTool(),
		ToolBackgroundStop:   BackgroundStopTool(),
	}
	args := `{"task_id":"bg_1"}`

	for name, tool := range tools {
		t.Run(name+" without a pool", func(t *testing.T) {
			if _, err := tool.Execute(context.Background(), args, &tooling.Env{}); err == nil {
				t.Fatal("expected a refusal when no pool is wired")
			}
		})
		t.Run(name+" while disabled", func(t *testing.T) {
			env := &tooling.Env{Background: bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())}
			_, err := tool.Execute(context.Background(), args, env)
			if err == nil {
				t.Fatal("expected a refusal while background execution is disabled")
			}
			if !strings.Contains(err.Error(), "disabled") {
				t.Fatalf("error %q does not explain that background execution is disabled", err)
			}
		})
	}
}

func TestRunCommandInBackgroundRefusesWhenDisabled(t *testing.T) {
	args, err := json.Marshal(runCommandArgs{Command: "sleep 1", Background: true})
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	env := &tooling.Env{Background: bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())}
	if _, err := executeRunCommandWithShell(context.Background(), string(args), env, platform.CurrentShell()); err == nil {
		t.Fatal("run_command should refuse to background while the feature is off")
	}
}

func TestBackgroundOutputReportsUnknownTasks(t *testing.T) {
	env := &tooling.Env{
		SessionID:         "s1",
		BackgroundEnabled: true,
		Background:        bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner()),
	}
	if _, err := BackgroundOutputTool().Execute(context.Background(), `{"task_id":"bg_404"}`, env); err == nil {
		t.Fatal("expected an error for an unknown task id")
	}
}

func TestHumanSeconds(t *testing.T) {
	cases := []struct {
		seconds int
		want    string
	}{
		{seconds: -5, want: "0s"},
		{seconds: 0, want: "0s"},
		{seconds: 45, want: "45s"},
		{seconds: 60, want: "1m"},
		{seconds: 95, want: "1m35s"},
		{seconds: 3600, want: "1h"},
		{seconds: 5400, want: "1h30m"},
	}
	for _, tc := range cases {
		if got := humanSeconds(tc.seconds); got != tc.want {
			t.Fatalf("humanSeconds(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestFormatTaskLineDescribesTheTask(t *testing.T) {
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	finished := start.Add(90 * time.Second)
	exit := 0

	running := bgtask.Snapshot{
		ID: "bg_1", Label: "make build", Status: bgtask.StatusRunning,
		StartedAt: start, ExpectedSeconds: 30,
	}
	line := formatTaskLine(running, start.Add(75*time.Second))
	for _, want := range []string{"bg_1", "running", "make build", "elapsed 1m15s", "estimated 30s", "overdue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q does not contain %q", line, want)
		}
	}

	done := bgtask.Snapshot{
		ID: "bg_2", Label: "go test ./...", Status: bgtask.StatusSucceeded,
		StartedAt: start, FinishedAt: &finished, ExitCode: &exit,
	}
	doneLine := formatTaskLine(done, start.Add(time.Hour))
	if !strings.Contains(doneLine, "exit 0") || !strings.Contains(doneLine, "elapsed 1m30s") {
		t.Fatalf("finished line %q should report the exit code and the total runtime", doneLine)
	}
	if strings.Contains(doneLine, "overdue") {
		t.Fatalf("finished line %q must not be overdue", doneLine)
	}
}
