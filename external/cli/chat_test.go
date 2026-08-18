//go:build cli

package cli

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

// Transcript blocks separate with one leading blank row (pi spacing): a user
// box must not sit flush against the header, and assistant text must not sit
// flush against the tool box above it.

func TestUserMessageStartsWithASeparatorRow(t *testing.T) {
	theme := newTheme("dark")
	lines := newUserMessage(theme, "hello").Render(40)
	if len(lines) < 2 {
		t.Fatalf("unexpected render: %q", lines)
	}
	if visible := tui.StripTerminalSequences(lines[0]); strings.TrimSpace(visible) != "" {
		t.Fatalf("first row must be a blank separator, got %q", lines[0])
	}
	if strings.Contains(lines[0], "48;") {
		t.Fatalf("the separator row must not carry the message background: %q", lines[0])
	}
}

// The `!!` prefix is recognised at the very start of the submitted buffer
// only: everything else keeps travelling to the model as ordinary text.
func TestParseLocalCommandRecognisesOnlyTheLeadingPrefix(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		command string
		ok      bool
	}{
		{"plain", "!!ls -la", "ls -la", true},
		{"space after prefix", "!!  git status  ", "git status", true},
		{"multi line body", "!!echo one\necho two", "echo one\necho two", true},
		{"empty command", "!!   ", "", true},
		{"escaped prefix is a prompt", `\!!ls`, "", false},
		{"single bang is a prompt", "!ls", "", false},
		{"prefix mid text", "run !!ls for me", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command, ok := parseLocalCommand(tc.text)
			if ok != tc.ok || command != tc.command {
				t.Fatalf("parseLocalCommand(%q) = (%q, %v), want (%q, %v)", tc.text, command, ok, tc.command, tc.ok)
			}
		})
	}
}

// The escape drops exactly one leading backslash, so a prompt can still start
// with the prefix. The editor trims the buffer before submit, so whitespace is
// not available as an escape.
func TestUnescapeLocalShellDropsOnlyTheLeadingBackslash(t *testing.T) {
	cases := map[string]string{
		`\!!careful`:  "!!careful",
		`\!!`:         "!!",
		`\\!!careful`: `\\!!careful`,
		`\!careful`:   `\!careful`,
		"!!ls":        "!!ls",
		`say \!!x`:    `say \!!x`,
	}
	for in, want := range cases {
		if got := unescapeLocalShell(in); got != want {
			t.Fatalf("unescapeLocalShell(%q) = %q, want %q", in, got, want)
		}
	}
}

// A finished command shows the tail of its output, because that is where a
// command's verdict lives; ctrl+o expands the whole capture from memory.
func TestShellBoxShowsTheOutputTailAndExpands(t *testing.T) {
	theme := newTheme("dark")
	box := newShellBox(theme, "seq 1 30")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	box.SetOutput(strings.Join(lines, "\n"), 0)
	box.Finish(0, nil)

	collapsed := renderedRows(box, 60)
	if !collapsed["line 30"] {
		t.Fatalf("collapsed box lost the tail:\n%v", collapsed)
	}
	if collapsed["line 1"] {
		t.Fatalf("collapsed box showed the head:\n%v", collapsed)
	}
	if !collapsed["... (20 earlier lines, ctrl+o to expand)"] {
		t.Fatalf("collapsed box hid the expand hint:\n%v", collapsed)
	}

	box.SetExpanded(true)
	expanded := renderedRows(box, 60)
	if !expanded["line 1"] || !expanded["line 30"] {
		t.Fatalf("expanded box lost output:\n%v", expanded)
	}
}

// renderedRows renders a component and returns its visible rows, trimmed of
// styling and the padding the box adds on every line.
func renderedRows(c tui.Component, width int) map[string]bool {
	rows := map[string]bool{}
	for _, line := range c.Render(width) {
		rows[strings.TrimSpace(tui.StripTerminalSequences(line))] = true
	}
	return rows
}

// A `!!` line is always consumed by the console, never forwarded to the
// model, even when it cannot run. These are the three refusals.
func TestLocalShellRefusalsNeverStartACommand(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		prepare func(a *App)
		want    string
	}{
		{
			name:    "remote session",
			text:    "!!ls",
			prepare: func(a *App) { a.remoteURL = "http://nas02:19980" },
			want:    "unavailable with --remote",
		},
		{
			name:    "turn in flight",
			text:    "!!ls",
			prepare: func(a *App) { a.turnActive = true },
			want:    "A turn is already running",
		},
		{
			name:    "command missing",
			text:    "!!   ",
			prepare: func(a *App) {},
			want:    "Type a command after !!",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			tc.prepare(a)
			if !a.dispatchLocalShell(tc.text) {
				t.Fatalf("%q was forwarded to the model", tc.text)
			}
			if a.shellActive || a.shellCmd != nil {
				t.Fatal("a refused command still started")
			}
			if got := transcriptText(a); !strings.Contains(got, tc.want) {
				t.Fatalf("transcript %q does not explain the refusal (%q)", got, tc.want)
			}
		})
	}
}

// While a local command runs the console takes nothing else on: every one of
// these must refuse without touching the (nil) backend, and none may open a
// modal, because a modal swallows the escape that stops the command.
func TestARunningLocalCommandRefusesEverythingElse(t *testing.T) {
	cases := []struct {
		name string
		act  func(a *App)
		want string
	}{
		{"another !!", func(a *App) { a.dispatchLocalShell("!!ls") }, "A local command is already running"},
		{"a prompt", func(a *App) { a.submitPrompt("hello") }, "A local command is running"},
		{"/new", func(a *App) { a.newSession() }, "A local command is running"},
		{"/resume", func(a *App) { a.openResumeSelector() }, "A local command is running"},
		{"the resume picker", func(a *App) { a.openResumePicker(nil) }, "A local command is running"},
		{"the model selector", func(a *App) { a.openModelSelector() }, "A local command is running"},
		{"/mode", func(a *App) { a.openModeSelector() }, "A local command is running"},
		{"/theme", func(a *App) { a.openThemeSelector() }, "A local command is running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.shellActive = true
			tc.act(a)
			if a.modal != nil {
				t.Fatal("a modal opened over a running command and would swallow escape")
			}
			if got := transcriptText(a); !strings.Contains(got, tc.want) {
				t.Fatalf("transcript %q does not refuse with %q", got, tc.want)
			}
		})
	}
}

// Escape must reach the process, not only the console state: the first one
// kills a real command through the terminate worker.
func TestEscapeKillsTheRunningCommand(t *testing.T) {
	commandShell := platform.CurrentShell()
	if commandShell.Kind != platform.ShellBash && commandShell.Kind != platform.ShellSh {
		t.Skipf("no portable long-running command for shell %q", commandShell.Kind)
	}
	a := newTestApp(t)
	cmd, err := shell.StartOperatorCommand("sleep 60", a.cfg.Paths.CWD)
	if err != nil {
		t.Fatal(err)
	}
	a.shellActive, a.shellCmd = true, cmd
	a.curShell = newShellBox(a.theme, "sleep 60")

	a.stopLocalShell()
	select {
	case <-cmd.Done():
	case <-time.After(15 * time.Second):
		cmd.Terminate(time.Second)
		t.Fatal("escape did not reach the process")
	}
	a.JoinWorkers(5 * time.Second)
	if rows := renderedRows(a.curShell, 60); !rows["stopping (escape again to leave it)"] {
		t.Fatalf("the block does not show that a stop was asked for:\n%v", rows)
	}
}

// A kill that never reaps its target (an uninterruptible process, a failed
// taskkill) must not wedge the console: the second escape takes it back.
func TestASecondEscapeReleasesAStuckCommand(t *testing.T) {
	a := newTestApp(t)
	a.shellActive = true
	a.curShell = newShellBox(a.theme, "sleep 300")

	a.stopLocalShell()
	if !a.shellStopping || !a.shellActive {
		t.Fatal("the first escape must ask the command to stop, not release it")
	}

	released := a.curShell
	a.stopLocalShell()
	if a.shellActive || a.shellStopping || a.curShell != nil {
		t.Fatal("the second escape must give the console back")
	}
	if rows := renderedRows(released, 60); !rows["left running (the console stopped waiting for it)"] {
		t.Fatalf("the released block still promises that escape does something:\n%v", rows)
	}
	// The status row wraps, so match a phrase that survives the wrap.
	if got := transcriptText(a); !strings.Contains(got, "releasing the console") {
		t.Fatalf("transcript %q hides that the process outlived the release", got)
	}
}

// newTestApp builds an App with the UI tree but no backend: the dispatch
// checks under test refuse before anything touches a session.
func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: t.TempDir()}}
	return newApp(cfg, nil, slog.New(slog.DiscardHandler), &bddTerminal{cols: 80, rows: 24}, "dark", true)
}

func transcriptText(a *App) string {
	var b strings.Builder
	for _, line := range a.chat.Render(80) {
		b.WriteString(tui.StripTerminalSequences(line))
		b.WriteString("\n")
	}
	return b.String()
}

// A non-zero exit is a normal outcome of an operator command, reported in the
// block rather than as an application error.
func TestShellBoxReportsNonZeroExit(t *testing.T) {
	theme := newTheme("dark")
	box := newShellBox(theme, "false")
	box.Finish(3, nil)
	if rendered := renderedRows(box, 60); !rendered["exit 3"] {
		t.Fatalf("box did not report the exit code:\n%v", rendered)
	}
}

// A command killed by a signal reports -1 whether or not the operator asked
// for it, so the block must not call an outside kill a cancellation.
func TestShellBoxSeparatesAStopFromASignalDeath(t *testing.T) {
	theme := newTheme("dark")

	stopped := newShellBox(theme, "sleep 300")
	stopped.RequestStop()
	stopped.Finish(-1, nil)
	if rendered := renderedRows(stopped, 60); !rendered["stopped"] {
		t.Fatalf("an operator stop must say so:\n%v", rendered)
	}

	signalled := newShellBox(theme, "sh -c 'kill -TERM $$'")
	signalled.Finish(-1, nil)
	rendered := renderedRows(signalled, 60)
	if rendered["stopped"] || !rendered["terminated"] {
		t.Fatalf("a signal death must not read as a cancellation:\n%v", rendered)
	}
}

func TestAssistantMessageStartsWithASeparatorRow(t *testing.T) {
	theme := newTheme("dark")
	msg := newAssistantMessage(theme, markdownTheme(theme, false), false)
	msg.AppendText("the answer")
	lines := msg.Render(40)
	if len(lines) < 2 {
		t.Fatalf("unexpected render: %q", lines)
	}
	if visible := tui.StripTerminalSequences(lines[0]); strings.TrimSpace(visible) != "" {
		t.Fatalf("first row must be a blank separator, got %q", lines[0])
	}
}
