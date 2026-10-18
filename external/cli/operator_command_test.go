//go:build cli

package cli

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func sampleOperatorRequest() acp.OperatorCommandRequest {
	return acp.OperatorCommandRequest{
		SessionID: "sess_1", RequestID: "oc_1", ToolCallID: "call_1",
		Command: "journalctl -u nginx\n  --since today", Reason: "run_command was refused by the unsafe-command check",
		CWD: "/srv/app", Shell: "bash", ShellPath: "/bin/bash", Host: "build-box",
	}
}

func decideWith(t *testing.T, keys ...string) string {
	t.Helper()
	m := newOperatorModal(newTheme("dark"), sampleOperatorRequest())
	got := ""
	m.OnDone = func(action string) { got = action }
	for _, k := range keys {
		m.HandleInput([]byte(k))
	}
	return got
}

// TestOperatorModalStartsOnDecline: an Enter typed before the modal appeared
// must never run anything.
func TestOperatorModalStartsOnDecline(t *testing.T) {
	if got := decideWith(t, "\r"); got != acp.OperatorActionDecline {
		t.Fatalf("enter on arrival = %q, want decline", got)
	}
	if got := decideWith(t, "\x1b"); got != acp.OperatorActionDecline {
		t.Fatalf("escape = %q, want decline", got)
	}
	// No letter runs it: a key typed for the editor while the modal opened
	// must not be read as a decision.
	for _, k := range []string{"r", "R", "y", " "} {
		if got := decideWith(t, k); got != "" {
			t.Fatalf("%q = %q, want no decision", k, got)
		}
	}
	if got := decideWith(t, "\x1b[B", "\r"); got != acp.OperatorActionRun {
		t.Fatalf("down, enter = %q, want run", got)
	}
}

// TestOperatorModalShowsTheWholeCall: every line of the command, the shell,
// the host, the folder and the agent's reason.
func TestOperatorModalShowsTheWholeCall(t *testing.T) {
	m := newOperatorModal(newTheme("dark"), sampleOperatorRequest())
	text := strings.Join(renderedLines(m, 100), "\n")
	for _, want := range []string{"journalctl -u nginx", "--since today", "bash", "build-box", "/srv/app", "unsafe-command check", "Decline", "Run"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the modal lacks %q:\n%s", want, text)
		}
	}
}

func renderedLines(m interface{ Render(int) []string }, width int) []string {
	var out []string
	for _, line := range m.Render(width) {
		out = append(out, tui.StripTerminalSequences(line))
	}
	return out
}

// TestEscapeStopsTheOperatorCommandFirst: while the operator's command runs,
// escape stops it (the turn goes on with its output); a second escape kills
// it; only after that does escape reach the turn.
func TestEscapeStopsTheOperatorCommandFirst(t *testing.T) {
	a := newTestApp(t)
	tk := acp.NewOperatorCommandTicket()
	if err := tk.Answer(acp.OperatorActionRun, ""); err != nil {
		t.Fatal(err)
	}
	a.sessionID = "sess_1"
	a.operatorRunning(tk, "sess_other")
	if a.handleGlobalKey([]byte("\x1b")) && tk.State() != acp.OperatorStateRunning {
		t.Fatal("escape in another session stopped this session's command")
	}
	a.operatorRunning(tk, "sess_1")
	if !a.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("escape was not taken by the running operator command")
	}
	if tk.State() != acp.OperatorStateStopping {
		t.Fatalf("after one escape: %s", tk.State())
	}
	a.handleGlobalKey([]byte("\x1b"))
	if tk.State() != acp.OperatorStateKilling {
		t.Fatalf("after two escapes: %s", tk.State())
	}
}

// TestRunHandsTheConsoleTheTicketAtOnce: an escape pressed right after Run
// stops the command, not the turn - the ticket is the console's from the Run
// itself, not from a later message.
func TestRunHandsTheConsoleTheTicketAtOnce(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_1"
	tk := acp.NewOperatorCommandTicket()
	a.openOperatorModal(opRequest{ctx: context.Background(), req: sampleOperatorRequest(), ticket: tk})
	m, ok := a.modal.(*operatorModal)
	if !ok {
		t.Fatalf("modal = %T", a.modal)
	}
	m.HandleInput([]byte("\x1b[B"))
	m.HandleInput([]byte("\r"))
	if tk.State() != acp.OperatorStateRunning {
		t.Fatalf("state after Run = %s", tk.State())
	}
	if !a.handleGlobalKey([]byte("\x1b")) || tk.State() != acp.OperatorStateStopping {
		t.Fatalf("escape right after Run: state %s", tk.State())
	}
}

func TestConsoleSendersOfferOperatorCommands(t *testing.T) {
	a := newTestApp(t)
	s := &sender{app: a}
	if acp.OperatorCommandsOf(s) == nil {
		t.Fatal("the console's sender does not offer operator commands")
	}
	if acp.OperatorCommandsOf(&lateBoundSender{inner: s}) == nil {
		t.Fatal("the late-bound sender hid the console's capability")
	}
	if acp.OperatorCommandsOf(&lateBoundSender{}) != nil {
		t.Fatal("a late-bound sender with no console offers operator commands")
	}
}

// TestToolBoxShowsTheOperatorCommandRunning: the frames' output window lands
// in the tool's box while the command runs.
func TestToolBoxShowsTheOperatorCommandRunning(t *testing.T) {
	a := newTestApp(t)
	a.applyLoopMessage(updateMsg{sessionID: a.sessionID, update: acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: "call_1", Title: "operator_command", Kind: "other", Status: "pending"}})
	a.applyLoopMessage(updateMsg{sessionID: a.sessionID, update: acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "call_1", Status: "in_progress",
		Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: `{"command":"make deploy","reason":"x"}`}}},
		Meta: map[string]interface{}{"coddy": map[string]interface{}{"operatorCommand": map[string]interface{}{
			"requestId": "oc_1", "phase": "running", "output": "step 1 of 3 done",
		}}},
	}})
	text := transcriptText(a)
	if !strings.Contains(text, "make deploy") || !strings.Contains(text, "step 1 of 3 done") {
		t.Fatalf("the transcript lacks the running command:\n%s", text)
	}
}

// TestLocalShellUsesTheOperatorShell: the console's !! runs in the shell of
// tools.operator_command, the one shell the operator runs commands in by hand.
func TestLocalShellUsesTheOperatorShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses $0 of a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	a := newTestApp(t)
	a.config().Tools.OperatorCommand.Shell = "sh"
	a.startLocalShell(`echo "shell=$0"`)
	deadline := time.Now().Add(10 * time.Second)
	for a.shellActive && time.Now().Before(deadline) {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if text := transcriptText(a); !regexp.MustCompile(`shell=(\S*/)?sh\s`).MatchString(text) {
		t.Fatalf("!! did not run in the configured shell:\n%s", text)
	}
}

// TestLocalShellRefusesAShellThatIsNotThere: no fallback to another shell.
func TestLocalShellRefusesAShellThatIsNotThere(t *testing.T) {
	a := newTestApp(t)
	a.config().Tools.OperatorCommand.Shell = "no-such-shell-xyz"
	a.startLocalShell("echo hi")
	if a.shellActive {
		t.Fatal("a command started in a shell that does not exist")
	}
	if text := transcriptText(a); !strings.Contains(text, "no-such-shell-xyz") {
		t.Fatalf("the block does not name the missing shell:\n%s", text)
	}
}
