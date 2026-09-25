package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
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

// foregroundEnv wires a real pool for the foreground timeout tests and stops
// whatever survives the assertions.
func foregroundEnv(t *testing.T, enabled bool) *tooling.Env {
	t.Helper()
	// The temp directory is claimed first so that its cleanup is registered
	// before the one below: cleanups run last-registered-first, and Windows
	// refuses to remove a directory an adopted command still has open as its
	// working directory.
	cwd := t.TempDir()
	pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	t.Cleanup(func() { pool.StopSession("unit-foreground") })
	return &tooling.Env{
		SessionID:         "unit-foreground",
		CWD:               cwd,
		BackgroundEnabled: enabled,
		Background:        pool,
	}
}

// runForegroundForTest runs one command with a tight timeout and reports how
// long the tool itself blocked, which is what the hang was about.
func runForegroundForTest(ctx context.Context, t *testing.T, command string, timeout int, env *tooling.Env) (string, time.Duration) {
	t.Helper()
	args, err := json.Marshal(runCommandArgs{Command: command, TimeoutSeconds: timeout})
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	out, err := executeRunCommandWithShell(ctx, string(args), env, platform.CurrentShell())
	took := time.Since(began)
	if err != nil {
		t.Fatalf("run_command returned an error (%v); the model would never see the output", err)
	}
	return out, took
}

func lastingCommandForTest(t *testing.T) string {
	t.Helper()
	command, err := sleepCommand(platform.CurrentShell().Kind, 60)
	if err != nil {
		t.Skipf("no sleep form for this shell: %v", err)
	}
	return command
}

func TestForegroundTimeoutAdoptsIntoTheBackgroundPool(t *testing.T) {
	env := foregroundEnv(t, true)
	before := time.Now()

	out, took := runForegroundForTest(context.Background(), t, lastingCommandForTest(t), 1, env)
	if took > 20*time.Second {
		t.Fatalf("run_command blocked for %s", took)
	}

	tasks := env.Background.List(env.SessionID)
	if len(tasks) != 1 {
		t.Fatalf("pool holds %d tasks, want exactly the adopted one; answer was %q", len(tasks), out)
	}
	snap := tasks[0]
	if !strings.Contains(out, snap.ID) {
		t.Fatalf("answer %q does not name the adopted task %q", out, snap.ID)
	}
	if snap.Status != bgtask.StatusRunning {
		t.Fatalf("adopted task is %q, want running: adoption must not kill the command", snap.Status)
	}
	// The foreground limit that just expired must never become the hard limit.
	if snap.TimeoutSeconds != 3600 {
		t.Fatalf("adopted task has a hard timeout of %ds, want the pool maximum of 3600s", snap.TimeoutSeconds)
	}
	if snap.StartedAt.After(before.Add(time.Second)) {
		t.Fatalf("adopted task started at %s, want the original foreground start around %s", snap.StartedAt, before)
	}
}

func TestForegroundAdoptionNoticeSurvivesTheOutputCeiling(t *testing.T) {
	snap := bgtask.Snapshot{ID: "bg_7", TimeoutSeconds: 3600}
	result := joinNotice(adoptionNotice(snap, 30), strings.Repeat("noise\n", 400))

	limit := 5
	trimmed := tooling.ApplyOutputLimit(result, "run_command", &tooling.Env{OutputLineLimits: map[string]int{"run_command": limit}})
	if !strings.Contains(trimmed, "bg_7") {
		t.Fatalf("the task id did not survive truncation: %q", trimmed)
	}
	if !strings.Contains(trimmed, "Do NOT start this work a second time") {
		t.Fatalf("the anti-retry warning did not survive truncation: %q", trimmed)
	}
}

func TestForegroundTimeoutWithBackgroundDisabledTerminatesTheGroup(t *testing.T) {
	env := foregroundEnv(t, false)

	out, took := runForegroundForTest(context.Background(), t, lastingCommandForTest(t), 1, env)
	if took > 20*time.Second {
		t.Fatalf("run_command blocked for %s", took)
	}
	if got := env.Background.List(env.SessionID); len(got) != 0 {
		t.Fatalf("pool holds %d tasks while background execution is disabled", len(got))
	}
	if !strings.Contains(out, "terminated") {
		t.Fatalf("answer %q does not say the command was terminated", out)
	}
	if !strings.Contains(out, "background: true") {
		t.Fatalf("answer %q does not tell the model what to do instead", out)
	}
}

func TestForegroundTimeoutWithoutAPoolTerminatesTheGroup(t *testing.T) {
	env := &tooling.Env{CWD: t.TempDir()}

	out, took := runForegroundForTest(context.Background(), t, lastingCommandForTest(t), 1, env)
	if took > 20*time.Second {
		t.Fatalf("run_command blocked for %s", took)
	}
	if !strings.Contains(out, "terminated") {
		t.Fatalf("answer %q does not say the command was terminated", out)
	}
}

// TestForegroundTimeoutDoesNotWaitOnAPipeHoldingGrandchild is the regression
// test for the original bug: killing the shell leaves whatever it spawned
// holding the output pipe, and cmd.Wait used to block on that forever.
func TestForegroundTimeoutDoesNotWaitOnAPipeHoldingGrandchild(t *testing.T) {
	commandShell := platform.CurrentShell()
	command, err := lastingChildCommand(commandShell, 60)
	if err != nil {
		t.Skipf("no lasting-child form for this shell: %v", err)
	}

	_, took := runForegroundForTest(context.Background(), t, command, 1, &tooling.Env{CWD: t.TempDir()})
	if took > 20*time.Second {
		t.Fatalf("run_command blocked for %s on a command whose child held the output pipe", took)
	}
}

func TestForegroundCancelledByContextIsNotAdopted(t *testing.T) {
	env := foregroundEnv(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, took := runForegroundForTest(ctx, t, lastingCommandForTest(t), 300, env)
	if took > 20*time.Second {
		t.Fatalf("a cancelled run_command blocked for %s", took)
	}
	if got := env.Background.List(env.SessionID); len(got) != 0 {
		t.Fatalf("a cancelled command was adopted into %d tasks; cancellation must terminate it", len(got))
	}
}

func TestForegroundCommandStillReportsExitStatus(t *testing.T) {
	env := &tooling.Env{CWD: t.TempDir()}

	out, _ := runForegroundForTest(context.Background(), t, "exit 3", 30, env)
	if !strings.Contains(out, "command failed:") || !strings.Contains(out, "exit status 3") {
		t.Fatalf("answer %q does not report the command's own exit code", out)
	}
}

// TestForegroundResultCarriesStderrWithoutRedirection guards the reason the
// output matters at all: a dev server reports a busy port or a missing binary on
// stderr, and the model must read it without having remembered to write 2>&1.
func TestForegroundResultCarriesStderrWithoutRedirection(t *testing.T) {
	commandShell := platform.CurrentShell()
	command, err := complainCommand(commandShell.Kind, "port 8080 is already in use")
	if err != nil {
		t.Skipf("no error-stream form for this shell: %v", err)
	}

	out, _ := runForegroundForTest(context.Background(), t, command, 30, &tooling.Env{CWD: t.TempDir()})
	if !strings.Contains(out, "port 8080 is already in use") {
		t.Fatalf("answer %q lost what the command wrote to stderr", out)
	}
}

func TestRunCommandDescriptionWarnsAgainstRedirectingStderr(t *testing.T) {
	for _, sh := range []platform.Shell{
		{Kind: platform.ShellPwsh, Path: "pwsh"},
		{Kind: platform.ShellBash, Path: "/bin/bash"},
	} {
		description := RunCommandToolForShell(sh).Definition.Description
		if !strings.Contains(description, "2>&1") {
			t.Fatalf("%s description does not tell the model that stderr is already captured: %q", sh.Kind, description)
		}
	}
}

// TestAdoptionNoticeForbidsASecondCopyOfTheWork covers what a live run showed:
// told only not to raise timeout_seconds, a model reaches for a variant of the
// same work instead - npm install --ignore-engines, then yarn install - and ends
// up with two installs fighting over one directory.
func TestAdoptionNoticeForbidsASecondCopyOfTheWork(t *testing.T) {
	notice := adoptionNotice(bgtask.Snapshot{ID: "bg_2", TimeoutSeconds: 3600}, 30)
	for _, want := range []string{
		"not the same command with a larger timeout_seconds",
		"not a variant with different flags",
		"not another tool that does the same job",
	} {
		if !strings.Contains(notice, want) {
			t.Fatalf("adoption notice does not rule out %q: %s", want, notice)
		}
	}
}

func TestRunCommandResultIsSanitised(t *testing.T) {
	commandShell := platform.CurrentShell()
	command, err := printCommand(commandShell.Kind, "\x1b[31mred\x1b[0m")
	if err != nil {
		t.Skipf("no printing form for this shell: %v", err)
	}

	out, _ := runForegroundForTest(context.Background(), t, command, 30, &tooling.Env{CWD: t.TempDir()})
	if !strings.Contains(out, "red") {
		t.Fatalf("answer %q lost the text under the escape sequences", out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("answer %q still carries escape sequences", out)
	}
}

func TestTerminateReachesTheWholeProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ProcessGroupAlive only checks the leader on Windows, so it cannot prove the group died")
	}
	commandShell := platform.CurrentShell()
	command, err := lastingChildCommand(commandShell, 60)
	if err != nil {
		t.Skipf("no lasting-child form for this shell: %v", err)
	}

	sc, err := startForeground(command, &tooling.Env{CWD: t.TempDir()}, commandShell)
	if err != nil {
		t.Fatal(err)
	}
	pid := sc.pid()
	if pid <= 0 {
		t.Fatal("started command reports no pid")
	}

	sc.terminate("terminated")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !platform.ProcessGroupAlive(pid, sc.processStartedAt) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process group %d is still alive after terminate", pid)
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

	// A running task the model will be woken for says so, so the model does
	// not spend a turn waiting on it; once it has ended there is nothing left
	// to wake, and the line says nothing of it.
	waking := running
	waking.NotifyOnFinish = true
	if line := formatTaskLine(waking, start.Add(10*time.Second)); !strings.Contains(line, "wakes you when it ends") {
		t.Fatalf("waking line %q does not say it wakes the model", line)
	}
	woke := done
	woke.NotifyOnFinish = true
	if line := formatTaskLine(woke, start.Add(time.Hour)); strings.Contains(line, "wakes you") {
		t.Fatalf("finished line %q still promises a wake", line)
	}
}

// The operator path is the console's `!!` prefix: same shell and process
// handling as run_command, but no permission gate, no session, and no pool.

func TestOperatorCommandReportsOutputAndExitCode(t *testing.T) {
	commandShell := platform.CurrentShell()
	command, err := printCommand(commandShell.Kind, "operator-ok")
	if err != nil {
		t.Skipf("no printing form for this shell: %v", err)
	}

	cmd, err := StartOperatorCommandForShell(command, t.TempDir(), commandShell)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cmd.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("operator command never finished")
	}
	if out := cmd.Output(); !strings.Contains(out, "operator-ok") {
		t.Fatalf("output %q lost the command output", out)
	}
	if code := cmd.ExitCode(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestOperatorCommandTerminateStopsTheCommand(t *testing.T) {
	commandShell := platform.CurrentShell()
	command, err := sleepCommand(commandShell.Kind, 60)
	if err != nil {
		t.Skipf("no sleep form for this shell: %v", err)
	}

	cmd, err := StartOperatorCommandForShell(command, t.TempDir(), commandShell)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	cmd.Terminate(time.Second)
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("terminate took %s; the console would freeze that long", took)
	}
	select {
	case <-cmd.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminated command was never reaped")
	}
	if code := cmd.ExitCode(); code == 0 {
		t.Fatal("a killed command must not report a successful exit")
	}
}

func TestTailBufferKeepsTheTailAndCountsWhatItDropped(t *testing.T) {
	tail := &tailBuffer{limit: 8}
	if _, err := tail.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got, _ := tail.Snapshot(); string(got) != "abcdef" {
		t.Fatalf("snapshot = %q, want the whole write", got)
	}
	if _, err := tail.Write([]byte("ghijkl")); err != nil {
		t.Fatal(err)
	}
	if got, _ := tail.Snapshot(); string(got) != "efghijkl" {
		t.Fatalf("snapshot = %q, want the last 8 bytes", got)
	}
	if got := tail.Dropped(); got != 4 {
		t.Fatalf("dropped = %d, want 4", got)
	}
	if got := cap(tail.buf); got > 64 {
		t.Fatalf("backing array grew to %d for an 8 byte limit", got)
	}
}

func TestTailBufferBoundsASingleOversizedWrite(t *testing.T) {
	tail := &tailBuffer{limit: 8}
	if _, err := tail.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if got, _ := tail.Snapshot(); string(got) != "89abcdef" {
		t.Fatalf("snapshot = %q, want the last 8 bytes", got)
	}
	if got := cap(tail.buf); got > 64 {
		t.Fatalf("a 16 byte write allocated %d bytes behind an 8 byte limit", got)
	}
	if got := tail.Dropped(); got != 8 {
		t.Fatalf("dropped = %d, want 8", got)
	}
}

// A capture is cut at both ends: the tail buffer drops the start of a rune,
// and a live poll catches the command mid-write. The trim runs before decoding
// so Windows never mistakes one broken byte for a code-page buffer - and it
// must leave a real code-page capture alone.
func TestTrimPartialUTF8CutsOnlyBrokenEnds(t *testing.T) {
	cases := []struct {
		name      string
		in        []byte
		truncated bool
		midWrite  bool
		want      []byte
	}{
		{"intact utf-8", []byte("дом"), false, false, []byte("дом")},
		{"head cut in half", []byte("\xbe\xd0\xbc"), true, false, []byte("м")},
		{"tail mid-write", []byte("до\xd0"), false, true, []byte("до")},
		{"both ends", []byte("\xbe\xd0\xbcа\xd0"), true, true, []byte("ма")},
		{"ascii", []byte("plain"), false, false, []byte("plain")},
		{"empty", []byte(""), false, false, []byte("")},
		// A finished command sends nothing more, so a trailing lead byte is a
		// whole code-page character (CP866 "р"), not half a rune.
		{"code page byte ends a finished capture", []byte("OK\xe0"), false, false, []byte("OK\xe0")},
		{"same bytes while still writing", []byte("OK\xe0"), false, true, []byte("OK")},
		// CP866 "Привет": invalid UTF-8 that trimming cannot rescue, so it
		// reaches DecodeOutput whole and is decoded as the code page there.
		{"code page stays whole", []byte{0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2}, false, false, []byte{0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2}},
		// CP866 "А" followed by ASCII: dropping the lead byte would make the
		// rest valid UTF-8, so only a real truncation may touch the head.
		{"code page char before ascii", []byte{0x80, 'O', 'K'}, false, false, []byte{0x80, 'O', 'K'}},
		{"same bytes after a real cut", []byte{0x80, 'O', 'K'}, true, false, []byte("OK")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trimPartialUTF8(tc.in, tc.truncated, tc.midWrite); string(got) != string(tc.want) {
				t.Fatalf("trimPartialUTF8(%q, %v, %v) = %q, want %q", tc.in, tc.truncated, tc.midWrite, got, tc.want)
			}
		})
	}
}

// idleHandle is a task handle with no process behind it: it settles when the
// test says so, the shape a subagent run has in the pool.
type idleHandle struct {
	done chan struct{}
	once sync.Once
}

func (h *idleHandle) Wait() (int, error)          { <-h.done; return 0, nil }
func (h *idleHandle) Stop(time.Duration) error    { h.finish(); return nil }
func (h *idleHandle) PID() int                    { return 0 }
func (h *idleHandle) ProcessStartedAt() time.Time { return time.Time{} }
func (h *idleHandle) finish()                     { h.once.Do(func() { close(h.done) }) }

// The pool tools are the model's view of its own work. A system task (the
// memory subagent) is the runtime's, so the listing omits it and the tools
// that read, wait on or stop a task refuse its id.
func TestBackgroundToolsHideSystemTasksFromTheModel(t *testing.T) {
	pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	env := &tooling.Env{SessionID: "s1", BackgroundEnabled: true, Background: pool}
	h := &idleHandle{done: make(chan struct{})}
	defer h.finish()
	snap, err := pool.Launch(bgtask.Spec{
		SessionID: "s1",
		Kind:      bgtask.KindAgent,
		Label:     "memory: what did we decide",
		Agent:     &bgtask.AgentInfo{Name: "memory", SessionID: "sess_mem", System: true},
	}, func(string, io.Writer) (bgtask.Handle, error) { return h, nil })
	if err != nil {
		t.Fatalf("Launch(): %v", err)
	}

	list, err := BackgroundListTool().Execute(context.Background(), `{}`, env)
	if err != nil {
		t.Fatalf("background_list: %v", err)
	}
	if strings.Contains(list, snap.ID) || strings.Contains(list, "memory:") {
		t.Fatalf("background_list shows the system task: %q", list)
	}
	if list != "No background tasks in this session." {
		t.Fatalf("background_list = %q, want the empty listing", list)
	}

	args := fmt.Sprintf(`{"task_id":%q,"timeout_seconds":1}`, snap.ID)
	for name, tool := range map[string]*tooling.Tool{
		ToolBackgroundOutput: BackgroundOutputTool(),
		ToolBackgroundWait:   BackgroundWaitTool(),
		ToolBackgroundStop:   BackgroundStopTool(),
	} {
		_, err := tool.Execute(context.Background(), args, env)
		if err == nil || !strings.Contains(err.Error(), "system task") || !strings.Contains(err.Error(), "memory") {
			t.Fatalf("%s on a system task: err = %v, want a refusal naming the system task", name, err)
		}
	}
	// The refusals changed nothing: the run is still going.
	if got, _ := pool.Get("s1", snap.ID); got.Status.Finished() {
		t.Fatalf("the system task was stopped by a refused tool call: %+v", got)
	}
}

// A surface with nothing subscribed to wake the agent - coddy -p - must not
// answer notify_on_finish with a promise it cannot keep: the model would end its
// turn waiting for a turn that never comes. Where a waker is subscribed the
// promise stands, and the task records the wake it will get.
func TestBackgroundNotifyIsHonestAboutWhetherAnythingCanWake(t *testing.T) {
	notify := true
	args, err := json.Marshal(runCommandArgs{Command: "sleep 0", Background: true, NotifyOnFinish: &notify})
	if err != nil {
		t.Fatal(err)
	}
	newEnv := func() *tooling.Env {
		return &tooling.Env{
			SessionID:         "s1",
			CWD:               t.TempDir(),
			BackgroundEnabled: true,
			Background:        bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner()),
			WakeableSession:   true,
		}
	}

	env := newEnv()
	t.Cleanup(func() { env.Background.StopSession(env.SessionID) })
	out, err := executeRunCommandWithShell(context.Background(), string(args), env, platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "You will be woken") {
		t.Fatalf("a pool nothing watches still promised a wake:\n%s", out)
	}
	for _, want := range []string{"Nothing will wake you", ToolBackgroundWait, ToolBackgroundOutput} {
		if !strings.Contains(out, want) {
			t.Fatalf("result %q does not mention %q", out, want)
		}
	}
	if tasks := env.Background.List(env.SessionID); len(tasks) != 1 || tasks[0].NotifyOnFinish {
		t.Fatalf("tasks = %+v, want one task that does not claim a wake", tasks)
	}

	woken := newEnv()
	t.Cleanup(func() { woken.Background.StopSession(woken.SessionID) })
	woken.Background.SubscribeKeyed(bgtask.WakeWatcherKey, func(bgtask.Snapshot) {})
	out, err = executeRunCommandWithShell(context.Background(), string(args), woken, platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "You will be woken") {
		t.Fatalf("a watched pool must still promise the wake it will deliver:\n%s", out)
	}
	if tasks := woken.Background.List(woken.SessionID); len(tasks) != 1 || !tasks[0].NotifyOnFinish {
		t.Fatalf("tasks = %+v, want one task recorded as waking the agent", tasks)
	}

	// A child or a scheduled run is sealed when its turn returns: the request
	// is refused in the same words even where a waker is subscribed.
	sealed := newEnv()
	sealed.WakeableSession = false
	t.Cleanup(func() { sealed.Background.StopSession(sealed.SessionID) })
	sealed.Background.SubscribeKeyed(bgtask.WakeWatcherKey, func(bgtask.Snapshot) {})
	out, err = executeRunCommandWithShell(context.Background(), string(args), sealed, platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "You will be woken") || !strings.Contains(out, "Nothing will wake you") {
		t.Fatalf("a sealed session was promised a wake:\n%s", out)
	}
}

func TestBackgroundCommandWakesByDefaultUnlessExplicitlyDisabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		wake bool
	}{
		{"omitted", `{"command":"echo done","background":true}`, true},
		{"explicit false", `{"command":"echo done","background":true,"notify_on_finish":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
			pool.SubscribeKeyed(bgtask.WakeWatcherKey, func(bgtask.Snapshot) {})
			env := &tooling.Env{SessionID: "s1", CWD: t.TempDir(), BackgroundEnabled: true, Background: pool, WakeableSession: true}
			t.Cleanup(func() { pool.StopSession("s1") })
			if _, err := executeRunCommandWithShell(context.Background(), tc.args, env, platform.CurrentShell()); err != nil {
				t.Fatal(err)
			}
			tasks := pool.List("s1")
			if len(tasks) != 1 || tasks[0].NotifyOnFinish != tc.wake {
				t.Fatalf("tasks = %+v, want notify_on_finish=%v", tasks, tc.wake)
			}
		})
	}
}

func TestCollectingOrStoppingATaskSuppressesItsWake(t *testing.T) {
	for _, name := range []string{"background_wait", "background_output", "background_stop"} {
		t.Run(name, func(t *testing.T) {
			pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
			h := &idleHandle{done: make(chan struct{})}
			env := &tooling.Env{SessionID: "s1", BackgroundEnabled: true, Background: pool}
			snap, err := pool.Launch(bgtask.Spec{SessionID: "s1", Kind: bgtask.KindAgent, Label: "child", NotifyOnFinish: true}, func(string, io.Writer) (bgtask.Handle, error) {
				return h, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pool.StopSession("s1") })
			if name != "background_stop" {
				h.finish()
				if _, err := pool.Wait(context.Background(), "s1", snap.ID, time.Second); err != nil {
					t.Fatal(err)
				}
			}
			var tool *tooling.Tool
			switch name {
			case "background_wait":
				tool = BackgroundWaitTool()
			case "background_output":
				tool = BackgroundOutputTool()
			case "background_stop":
				tool = BackgroundStopTool()
			}
			if _, err := tool.Execute(context.Background(), fmt.Sprintf(`{"task_id":%q}`, snap.ID), env); err != nil {
				t.Fatal(err)
			}
			if pool.WakePending("s1", snap.ID) {
				t.Fatal("a result the model already handled must not wake")
			}
		})
	}
}

func TestAdoptedForegroundCommandWakesWhenItFinishes(t *testing.T) {
	command, err := sleepCommand(platform.CurrentShell().Kind, 2)
	if err != nil {
		t.Skip(err)
	}
	pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
	finished := make(chan bgtask.Snapshot, 1)
	pool.SubscribeKeyed(bgtask.WakeWatcherKey, func(s bgtask.Snapshot) {
		if s.Status.Finished() {
			finished <- s
		}
	})
	env := &tooling.Env{SessionID: "s1", CWD: t.TempDir(), BackgroundEnabled: true, Background: pool, WakeableSession: true}
	t.Cleanup(func() { pool.StopSession("s1") })
	out, err := executeRunCommandWithShell(context.Background(), fmt.Sprintf(`{"command":%q,"timeout_seconds":1}`, command), env, platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "background task") || !strings.Contains(out, "You will be woken") {
		t.Fatalf("foreground timeout did not promise the adopted task's wake: %q", out)
	}
	select {
	case snap := <-finished:
		if !snap.NotifyOnFinish || snap.Status != bgtask.StatusSucceeded {
			t.Fatalf("adopted task completion = %+v, want a waking success", snap)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adopted task never finished")
	}
}
