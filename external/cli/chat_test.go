//go:build cli

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
	cmd, err := shell.StartOperatorCommand("sleep 60", a.config().Paths.CWD)
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

// --- run.go: the console turn agent and the staged config flow ---

// stagedConfigBackend stands in for an OpenAI-compatible server answering
// blocking (stream: false) requests. It records the tool names every request
// offered and, when scripted, drives one self-configuration turn: config_set,
// then config_commit, then a plain answer. Unscripted it answers at once.
type stagedConfigBackend struct {
	script bool

	mu    sync.Mutex
	tools [][]string
}

func (b *stagedConfigBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &body)
	names := make([]string, 0, len(body.Tools))
	for _, tool := range body.Tools {
		names = append(names, tool.Function.Name)
	}
	b.mu.Lock()
	b.tools = append(b.tools, names)
	turn := len(b.tools)
	b.mu.Unlock()

	message := map[string]any{"role": "assistant", "content": "Done."}
	finish := "stop"
	if b.script && turn <= 2 {
		call := map[string]any{"name": "config_set", "arguments": `{"commands":["set agent.max_turns=19"]}`}
		if turn == 2 {
			call = map[string]any{"name": "config_commit", "arguments": "{}"}
		}
		message = map[string]any{
			"role": "assistant", "content": "",
			"tool_calls": []map[string]any{{"id": fmt.Sprintf("call_%d", turn), "type": "function", "function": call}},
		}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": fmt.Sprintf("chatcmpl-%d", turn), "object": "chat.completion", "model": "stub",
		"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": message}},
		"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
}

func (b *stagedConfigBackend) offered() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]string(nil), b.tools...)
}

// newConsoleOverStub writes a config.yaml in a temp home whose only model is
// served by the stub backend and builds the console exactly as Run does: real
// manager, real store, real agent, no LLM.
func newConsoleOverStub(t *testing.T, backend http.Handler) (*App, *config.Config) {
	t.Helper()
	ts := httptest.NewServer(backend)
	t.Cleanup(ts.Close)
	home, cwd := t.TempDir(), t.TempDir()
	yaml := "agent:\n  model: stub/model\n  max_turns: 35\n" +
		"providers:\n  - name: stub\n    type: openai\n    api_base: " + ts.URL + "\n    api_key: test\n" +
		"models:\n  - model: stub/model\n    max_tokens: 200\n    stream: false\n" +
		"tools:\n  permission_mode: bypass\n" +
		"rules:\n  auto_discover: false\n"
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home, CWD: cwd, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	store := &session.FileStore{Root: filepath.Join(home, "sessions")}
	app := buildApp(cfg, store, slog.New(slog.DiscardHandler), &bddTerminal{cols: 80, rows: 24}, "dark", true)
	t.Cleanup(app.Close)
	return app, cfg
}

// runConsoleTurn opens a session and runs one prompt through the manager, so
// the turn goes through the runner buildApp installed.
func runConsoleTurn(t *testing.T, app *App, text string) {
	t.Helper()
	ctx := context.Background()
	res, err := app.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: app.config().Paths.CWD})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	params := acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: text}}}
	if _, err := app.mgr.HandleSessionPromptWithSender(ctx, params, app.Sender(), nil); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
}

// The console offers the whole staged config family, so the model can change
// Coddy's own configuration from the terminal as it can over ACP and HTTP.
// Without the runtime reloader the agent hides everything but config_get.
func TestConsoleTurnOffersStagedConfigTools(t *testing.T) {
	backend := &stagedConfigBackend{}
	app, _ := newConsoleOverStub(t, backend)
	runConsoleTurn(t, app, "which config tools do you have?")
	offered := backend.offered()
	if len(offered) == 0 {
		t.Fatal("the stub backend saw no request")
	}
	for _, name := range []string{"config_get", "config_set", "config_changes", "config_commit", "config_revert", "config_rollback"} {
		if !slices.Contains(offered[0], name) {
			t.Errorf("%s missing from the console turn's tools: %v", name, offered[0])
		}
	}
}

// A committed change hot-reloads the console itself: the app reads the new
// file and queues the refresh of its model catalog, footer, and header.
func TestConsoleAdoptsTheConfigAfterCommit(t *testing.T) {
	backend := &stagedConfigBackend{script: true}
	app, startup := newConsoleOverStub(t, backend)
	runConsoleTurn(t, app, "set agent.max_turns to 19")
	if got := len(backend.offered()); got != 3 {
		t.Fatalf("stub calls = %d, want stage, commit, and the final answer", got)
	}
	if app.config() == startup {
		t.Fatal("the console still holds the startup config after config_commit")
	}
	if got := app.config().Agent.MaxTurns; got != 19 {
		t.Fatalf("live agent.max_turns = %d, want 19", got)
	}

	var reloaded *updateMsg
	for reloaded == nil {
		select {
		case msg := <-app.updatesCh:
			if _, ok := msg.update.(configReloaded); ok {
				reloaded = &msg
			}
		default:
			t.Fatal("no configReloaded update reached the UI queue")
		}
	}
	app.applyLoopMessage(*reloaded)
	if ids := app.modelIDs(); !slices.Contains(ids, "stub/model") {
		t.Fatalf("model catalog after the reload = %v", ids)
	}
}
