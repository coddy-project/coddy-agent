package shell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// operatorClient plays the operator: it records what it was shown and
// answers through a real ticket, the way every surface does.
type operatorClient struct {
	mu        sync.Mutex
	available bool
	answer    string // run | decline
	afterRun  func(t *acp.OperatorCommandTicket)
	requests  []acp.OperatorCommandRequest
	frames    []acp.ToolCallStatusUpdate
}

func (c *operatorClient) SendSessionUpdate(_ string, u interface{}) error {
	if f, ok := u.(acp.ToolCallStatusUpdate); ok {
		c.mu.Lock()
		c.frames = append(c.frames, f)
		c.mu.Unlock()
	}
	return nil
}
func (c *operatorClient) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}
func (c *operatorClient) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}
func (c *operatorClient) OperatorCommandsAvailable() bool { return c.available }
func (c *operatorClient) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	answer := c.answer
	afterRun := c.afterRun
	c.mu.Unlock()
	tk := acp.NewOperatorCommandTicket()
	if err := tk.Answer(answer, "not here"); err != nil {
		return nil, err
	}
	d, err := tk.Wait(ctx)
	if err == nil && answer == acp.OperatorActionRun && afterRun != nil {
		go afterRun(tk)
	}
	return d, err
}

func (c *operatorClient) shown() []acp.OperatorCommandRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.OperatorCommandRequest(nil), c.requests...)
}

func (c *operatorClient) phases() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, f := range c.frames {
		if m, ok := operatorMeta(f); ok {
			out = append(out, m["phase"].(string))
		}
	}
	return out
}

func operatorMeta(f acp.ToolCallStatusUpdate) (map[string]interface{}, bool) {
	coddy, ok := f.Meta["coddy"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	m, ok := coddy["operatorCommand"].(map[string]interface{})
	return m, ok
}

func operatorEnv(t *testing.T, client acp.UpdateSender) *tooling.Env {
	t.Helper()
	return &tooling.Env{
		CWD:              t.TempDir(),
		SessionID:        "sess_test",
		ToolCallID:       "call_1",
		Sender:           client,
		OperatorCommand:  &tooling.OperatorCommandSettings{Enabled: true},
		OperatorDeclined: tooling.NewOperatorDeclines(),
	}
}

func operatorArgs(command string) string {
	b, _ := json.Marshal(map[string]string{"command": command, "reason": "run_command was refused"})
	return string(b)
}

func runOperatorTool(t *testing.T, env *tooling.Env, args string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return OperatorCommandTool(env.OperatorCommand).Execute(ctx, args, env)
}

func echoCommand(word string) string {
	if k := platform.CurrentShell().Kind; k == platform.ShellPwsh || k == platform.ShellPowerShell {
		return "Write-Output " + word
	}
	return "echo " + word
}

func TestOperatorCommandRunsAndReturnsTheOutput(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	out, err := runOperatorTool(t, env, operatorArgs(echoCommand("operator-ok")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "operator-ok") || !strings.Contains(out, "exit code 0") {
		t.Fatalf("result = %q", out)
	}
	shown := client.shown()
	if len(shown) != 1 || shown[0].Command != echoCommand("operator-ok") {
		t.Fatalf("shown = %+v", shown)
	}
	req := shown[0]
	if req.Shell == "" || req.ShellPath == "" || req.Host == "" || !filepath.IsAbs(req.CWD) || req.Reason == "" {
		t.Fatalf("the request lacks what the card shows: %+v", req)
	}
	if !strings.HasPrefix(req.RequestID, "oc_") || req.ToolCallID != "call_1" {
		t.Fatalf("ids = %q %q", req.RequestID, req.ToolCallID)
	}
	phases := client.phases()
	if len(phases) == 0 || phases[0] != "running" || phases[len(phases)-1] != "ended" {
		t.Fatalf("frame phases = %v", phases)
	}
}

func TestOperatorCommandDeclineRunsNothing(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionDecline}
	env := operatorEnv(t, client)
	marker := filepath.Join(env.CWD, "ran")
	command := "echo x > ran"
	out, err := runOperatorTool(t, env, operatorArgs(command))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "declined") || !strings.Contains(out, "not here") {
		t.Fatalf("result = %q", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a declined command ran")
	}
	// The same command again in the same turn is refused without asking.
	out, err = runOperatorTool(t, env, operatorArgs(command))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already declined") || len(client.shown()) != 1 {
		t.Fatalf("a repeat after a decline: %q, asked %d times", out, len(client.shown()))
	}
}

func TestOperatorCommandNeedsAnOperator(t *testing.T) {
	for name, sender := range map[string]acp.UpdateSender{
		"no sender":     nil,
		"nobody there":  &operatorClient{available: false, answer: acp.OperatorActionRun},
		"no capability": plainSender{},
	} {
		t.Run(name, func(t *testing.T) {
			env := operatorEnv(t, sender)
			if sender == nil {
				env.Sender = nil
			}
			_, err := runOperatorTool(t, env, operatorArgs(echoCommand("x")))
			if err == nil || !strings.Contains(err.Error(), "needs an operator") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

type plainSender struct{}

func (plainSender) SendSessionUpdate(string, interface{}) error { return nil }
func (plainSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return nil, nil
}
func (plainSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, nil
}

// TestOperatorCommandRefusesWhatWouldDisplayDifferently: the operator must
// read exactly what the shell gets.
func TestOperatorCommandRefusesWhatWouldDisplayDifferently(t *testing.T) {
	for name, command := range map[string]string{
		"carriage return": "echo safe\rrm -rf ~",
		"escape":          "echo \x1b[2Ksafe",
		"bidi override":   "echo \u202eevil",
		"zero width":      "ec\u200bho x",
		"bom":             "\ufeffecho x",
		"invisible times": "echo a\u2063b",
		"tag character":   "echo a\U000E0041",
		"mongolian vowel": "echo a\u180eb",
		"grapheme joiner": "echo a\u034fb",
		"variation sel":   "echo a\ufe0fb",
		"hangul filler":   "echo a\u3164b",
		"line separator":  "echo a\u2028rm -rf ~",
		"paragraph sep":   "echo a\u2029b",
		"empty":           "   ",
	} {
		t.Run(name, func(t *testing.T) {
			client := &operatorClient{available: true, answer: acp.OperatorActionRun}
			_, err := runOperatorTool(t, operatorEnv(t, client), operatorArgs(command))
			if err == nil {
				t.Fatal("accepted")
			}
			if len(client.shown()) != 0 {
				t.Fatal("the operator was shown a command that was refused")
			}
		})
	}
	client := &operatorClient{available: true, answer: acp.OperatorActionDecline}
	if _, err := runOperatorTool(t, operatorEnv(t, client), operatorArgs("printf 'a\tb'\necho done")); err != nil {
		t.Fatalf("a tab and a newline are ordinary: %v", err)
	}
}

func TestOperatorCommandStopInterruptsTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Stop on Windows terminates the group; covered by the kill path")
	}
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	client.afterRun = func(tk *acp.OperatorCommandTicket) {
		time.Sleep(500 * time.Millisecond)
		_ = tk.Answer(acp.OperatorActionStop, "")
	}
	env := operatorEnv(t, client)
	out, err := runOperatorTool(t, env, operatorArgs(`trap 'echo trapped; exit 3' INT; echo started; while :; do sleep 0.1; done`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "trapped") || !strings.Contains(out, "started") {
		t.Fatalf("result = %q", out)
	}
	if phases := client.phases(); phases[len(phases)-1] != "stopped" {
		t.Fatalf("phases = %v", phases)
	}
}

func TestOperatorCommandTurnCancelKillsTheCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX marker")
	}
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := OperatorCommandTool(env.OperatorCommand).Execute(ctx, operatorArgs(`trap '' INT; sleep 30`), env)
	if err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("cancel took %s", time.Since(start))
	}
}

func TestOperatorCommandDescriptionNamesTheShell(t *testing.T) {
	tool := OperatorCommandTool(&tooling.OperatorCommandSettings{Enabled: true, Shell: "sh"})
	if !strings.Contains(tool.Definition.Description, "sh") {
		t.Fatalf("description: %s", tool.Definition.Description)
	}
	if tool.RequiresPermission {
		t.Fatal("operator_command must not go through the permission gate: the operator's click is its gate")
	}
	if tool.Definition.Name != OperatorCommandToolName {
		t.Fatalf("name = %q", tool.Definition.Name)
	}
}

func TestOperatorCommandUnknownShellIsAnError(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	env.OperatorCommand = &tooling.OperatorCommandSettings{Enabled: true, Shell: "no-such-shell-xyz"}
	_, err := runOperatorTool(t, env, operatorArgs("echo x"))
	if err == nil || !strings.Contains(err.Error(), "no-such-shell-xyz") {
		t.Fatalf("err = %v", err)
	}
	if len(client.shown()) != 0 {
		t.Fatal("the operator was asked to run a command in a shell that does not exist")
	}
}

// fakeRun is an operatorRun the test drives by hand.
type fakeRun struct {
	mu         sync.Mutex
	done       chan struct{}
	closed     bool
	kills      int
	exitOnKill bool
}

func newFakeRun() *fakeRun { return &fakeRun{done: make(chan struct{})} }

func (f *fakeRun) Output() string        { return "partial" }
func (f *fakeRun) Dropped() int64        { return 0 }
func (f *fakeRun) Done() <-chan struct{} { return f.done }
func (f *fakeRun) ExitCode() int         { return 130 }
func (f *fakeRun) Interrupt()            {}
func (f *fakeRun) Kill(time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills++
	if f.exitOnKill {
		f.exitLocked()
	}
}
func (f *fakeRun) exit() { f.mu.Lock(); defer f.mu.Unlock(); f.exitLocked() }
func (f *fakeRun) exitLocked() {
	if !f.closed {
		f.closed = true
		close(f.done)
	}
}
func (f *fakeRun) killCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.kills }

func runningTicket(t *testing.T) (*acp.OperatorCommandTicket, *acp.OperatorCommandDecision) {
	t.Helper()
	tk := acp.NewOperatorCommandTicket()
	if err := tk.Answer(acp.OperatorActionRun, ""); err != nil {
		t.Fatal(err)
	}
	d, err := tk.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tk, d
}

// TestOperatorStopIsNotLostToTheShellsExit: when the Stop and the shell's exit
// are ready at once, select picks either; the accepted Stop must still be
// what the result says, and the group must still be terminated.
func TestOperatorStopIsNotLostToTheShellsExit(t *testing.T) {
	env := operatorEnv(t, &operatorClient{available: true})
	req := acp.OperatorCommandRequest{SessionID: "s", RequestID: "oc_1", ToolCallID: "c", Shell: "bash", Host: "h", CWD: "/w"}
	for i := 0; i < 64; i++ {
		tk, d := runningTicket(t)
		if err := tk.Answer(acp.OperatorActionStop, ""); err != nil {
			t.Fatal(err)
		}
		run := newFakeRun()
		run.exit()
		out, err := superviseOperatorRun(context.Background(), env, req, "{}", run, d)
		d.Done()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "stopped") {
			t.Fatalf("iteration %d: an accepted Stop was reported as a plain end: %q", i, out)
		}
		if run.killCount() == 0 {
			t.Fatalf("iteration %d: the group of a stopped command was not terminated", i)
		}
	}
}

// TestOperatorCancelKeepsTheRequestOpenUntilTheProcessEnds: a cancelled turn
// returns after the drain, but the request stays running - and a Stop still
// reaches the process - until the process has really ended.
func TestOperatorCancelKeepsTheRequestOpenUntilTheProcessEnds(t *testing.T) {
	old := operatorCancelDrain
	operatorCancelDrain = 50 * time.Millisecond
	t.Cleanup(func() { operatorCancelDrain = old })
	env := operatorEnv(t, &operatorClient{available: true})
	req := acp.OperatorCommandRequest{SessionID: "s", RequestID: "oc_2", ToolCallID: "c", Shell: "bash", Host: "h", CWD: "/w"}
	tk, d := runningTicket(t)
	run := newFakeRun() // ignores every signal
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := superviseOperatorRunAndRelease(ctx, env, req, "{}", run, d); err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	if st := tk.State(); st == acp.OperatorStateEnded {
		t.Fatal("the request ended while its process still runs")
	}
	run.mu.Lock()
	run.exitOnKill = true
	run.mu.Unlock()
	if err := tk.Answer(acp.OperatorActionStop, ""); err != nil {
		t.Fatalf("a stop after the cancel: %v", err)
	}
	_ = tk.Answer(acp.OperatorActionStop, "")
	select {
	case <-tk.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not end once its process did")
	}
}

// TestOperatorCancelReportsAStillRunningCommand: when the drain expires the
// card hears "stopping" - the request is open, Stop still works - and
// "stopped" only once the process has ended.
func TestOperatorCancelReportsAStillRunningCommand(t *testing.T) {
	old := operatorCancelDrain
	operatorCancelDrain = 50 * time.Millisecond
	t.Cleanup(func() { operatorCancelDrain = old })
	client := &operatorClient{available: true}
	env := operatorEnv(t, client)
	req := acp.OperatorCommandRequest{SessionID: "s", RequestID: "oc_3", ToolCallID: "c", Shell: "bash", Host: "h", CWD: "/w"}
	tk, d := runningTicket(t)
	run := newFakeRun()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = superviseOperatorRunAndRelease(ctx, env, req, "{}", run, d)
	phases := client.phases()
	if last := phases[len(phases)-1]; last != OperatorPhaseStopping {
		t.Fatalf("phases after the drain = %v, want stopping last", phases)
	}
	run.exit()
	select {
	case <-tk.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not end")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := client.phases(); p[len(p)-1] == OperatorPhaseStopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stopped frame once the process ended: %v", client.phases())
}

// TestOperatorCommandRefusesAFolderThatWouldDisplayDifferently: the folder is
// on the card too.
func TestOperatorCommandRefusesAFolderThatWouldDisplayDifferently(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	odd := filepath.Join(env.CWD, "a\u202eb")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Skipf("the filesystem refuses the name: %v", err)
	}
	b, _ := json.Marshal(map[string]string{"command": "echo x", "reason": "r", "cwd": odd})
	if _, err := runOperatorTool(t, env, string(b)); err == nil || !strings.Contains(err.Error(), "cwd") {
		t.Fatalf("err = %v, want the folder refused", err)
	}
	if len(client.shown()) != 0 {
		t.Fatal("the operator was shown a folder that reads differently")
	}
}

// TestOperatorCommandRefusesAMultiLineCommandForCmd: cmd /c runs the first
// line only, so the operator would approve lines that never run.
func TestOperatorCommandRefusesAMultiLineCommandForCmd(t *testing.T) {
	if err := validateOperatorCommandFor("echo a\necho b", platform.ShellCmd); err == nil {
		t.Fatal("a multi-line command for cmd was accepted")
	}
	if err := validateOperatorCommandFor("echo a\necho b", platform.ShellBash); err != nil {
		t.Fatalf("bash takes lines: %v", err)
	}
}

// TestOperatorSingleStopEscalates: one Stop of a command that ignores the
// interrupt still ends it, after the grace, without a second press.
func TestOperatorSingleStopEscalates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals are a Unix notion")
	}
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	client.afterRun = func(tk *acp.OperatorCommandTicket) {
		time.Sleep(300 * time.Millisecond)
		_ = tk.Answer(acp.OperatorActionStop, "")
	}
	start := time.Now()
	out, err := runOperatorTool(t, operatorEnv(t, client), operatorArgs(`trap '' INT; echo ready; sleep 30`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "ready") {
		t.Fatalf("result = %q", out)
	}
	if took := time.Since(start); took < operatorStopGrace || took > 15*time.Second {
		t.Fatalf("the stop took %s, want the grace (%s) and not much more", took, operatorStopGrace)
	}
}
