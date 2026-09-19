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
// A folder whose path holds a space is inserted quoted, with the quote closed
// ahead of the cursor: sent as it stands, the text still names the folder,
// and the file picked next takes that quote over instead of doubling it.
func TestQuotedFolderMentionIsClosedAheadOfTheCursor(t *testing.T) {
	p := &completionProvider{}
	lines, line, col := p.Apply([]string{`see @my`}, 0, len(`see @my`), tui.AutocompleteItem{Value: `@"my folder/`})
	if lines[0] != `see @"my folder/"` || col != len(`see @"my folder/`) {
		t.Fatalf("folder step: %q, cursor %d", lines[0], col)
	}
	lines, _, col = p.Apply(lines, line, col, tui.AutocompleteItem{Value: `@"my folder/a b.md"`})
	if lines[0] != `see @"my folder/a b.md" ` || col != len(lines[0]) {
		t.Fatalf("file step: %q, cursor %d", lines[0], col)
	}
}

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
		{"/permissions", func(a *App) { a.openPermissionSelector() }, "A local command is running"},
		{"/plan", func(a *App) { a.dispatchSlash("/plan") }, "A local command is running"},
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
	// Only completions are turns. The session manager also reads the model
	// listing for the context window; this server has none, like many.
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
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

// toolBoxText renders a tool box and strips styling, so assertions read what
// the operator reads.
func toolBoxText(t *testing.T, tb *toolBox, width int) string {
	t.Helper()
	return tui.StripTerminalSequences(strings.Join(tb.Render(width), "\n"))
}

// The question tool answers the model in JSON. Printing that JSON into the
// transcript hands the operator `{"answers":[["..."]]}` for a decision they
// just made; the box shows the question with the answer instead, the way the
// SPA timeline does.
func TestQuestionToolBoxShowsTheAnsweredQuestions(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-1", "question", "other", nil)
	tb.SetArgs(`{"questions":[` +
		`{"header":"Swarm topology","question":"Where should the relay live?","options":[{"label":"On nas02"}]},` +
		`{"question":"How is the node started?","options":[{"label":"A systemd service"}]}]}`)
	tb.SetStatus("completed", `{"answers":[["On nas02"],["A systemd service"]]}`, 0, 0)

	text := toolBoxText(t, tb, 80)
	for _, want := range []string{
		"Where should the relay live?", "On nas02",
		"How is the node started?", "A systemd service",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing from the box:\n%s", want, text)
		}
	}
	if strings.Contains(text, "answers") || strings.Contains(text, "[[") {
		t.Fatalf("raw result JSON still reaches the transcript:\n%s", text)
	}

	// The arguments also travel behind an "Arguments:" label, and a model may
	// hand the question list over as one object instead of an array.
	labelled := newToolBox(newTheme("dark"), "call-1b", "question", "other", nil)
	labelled.SetArgs(`Arguments: {"questions":{"question":"Where should the relay live?","options":[{"label":"On nas02"}]}}`)
	labelled.SetStatus("completed", `{"answers":[["On nas02"]]}`, 0, 0)
	if text := toolBoxText(t, labelled, 80); !strings.Contains(text, "Where should the relay live?") {
		t.Fatalf("labelled arguments lost the question:\n%s", text)
	}
}

// A dismissed question answers with a null list: say so rather than print it.
func TestQuestionToolBoxReportsADismissedQuestion(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-2", "question", "other", nil)
	tb.SetArgs(`{"questions":[{"question":"Pick one","options":[{"label":"A"}]}]}`)
	tb.SetStatus("completed", `{"answers":null}`, 0, 0)

	text := toolBoxText(t, tb, 80)
	if !strings.Contains(text, "Pick one") || !strings.Contains(text, "no answer") {
		t.Fatalf("dismissed question renders as %q", text)
	}
	if strings.Contains(text, "null") {
		t.Fatalf("raw result JSON still reaches the transcript:\n%s", text)
	}
}

// Only the question tool is reformatted, and only when its result parses: any
// other body reaches the box unchanged.
func TestToolBoxLeavesOtherResultsAlone(t *testing.T) {
	other := newToolBox(newTheme("dark"), "call-3", "read", "read", nil)
	other.SetStatus("completed", `{"answers":[["A"]]}`, 0, 0)
	if text := toolBoxText(t, other, 80); !strings.Contains(text, `{"answers":[["A"]]}`) {
		t.Fatalf("a read result was rewritten:\n%s", text)
	}

	broken := newToolBox(newTheme("dark"), "call-4", "question", "other", nil)
	broken.SetStatus("failed", "questions must be non-empty", 0, 0)
	if text := toolBoxText(t, broken, 80); !strings.Contains(text, "questions must be non-empty") {
		t.Fatalf("a question error was rewritten:\n%s", text)
	}
}

// A delegated run is the one call whose work happens somewhere else: the
// console shows nothing of the child's turn until it reports back. The box
// therefore says which subagent took the task and what it was told to do,
// the way the SPA's agent card does.
func TestSpawnAgentToolBoxNamesTheAgentAndShowsTheDelegatedPrompt(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-5", "spawn_agent", "other", nil)
	tb.SetArgs(`{"agent":"general","description":"review the diff",` +
		`"prompt":"Read internal/agent/react.go\nand report what changed","timeout_seconds":300}`)
	tb.SetStatus("in_progress", "", 0, 0)

	text := toolBoxText(t, tb, 100)
	for _, want := range []string{
		"Read internal/agent/react.go",
		"and report what changed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing from the box:\n%s", want, text)
		}
	}
	// The subagent, the task and the options the run was launched with belong
	// on the title row, each behind the same separator.
	title := tui.StripTerminalSequences(tb.title())
	if title != "spawn_agent general · review the diff · timeout 300s" {
		t.Fatalf("title row reads %q", title)
	}
}

// The arguments also travel behind an "Arguments:" label, and a delegation is
// the one call the console reads several fields of, so the envelope has to
// come off for the whole object rather than for a single key.
func TestSpawnAgentToolBoxReadsLabelledArguments(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-15", "spawn_agent", "other", nil)
	tb.SetArgs(`Arguments: {"agent":"general","description":"check the docs","prompt":"Read the page"}`)
	text := toolBoxText(t, tb, 100)
	for _, want := range []string{"spawn_agent general · check the docs", "Read the page"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing from a labelled delegation:\n%s", want, text)
		}
	}
}

// A finished call whose result never made it to disk still says so, even when
// it printed nothing: the guard is about the call being over, not about
// whether there was a preview.
func TestToolBoxReportsAMissingResultOnlyOnceTheCallIsOver(t *testing.T) {
	missing := func(string) (string, bool) { return "", false }

	running := newToolBox(newTheme("dark"), "call-16", "read", "read", missing)
	running.SetStatus("in_progress", "", 0, 0)
	running.SetExpanded(true)
	if text := toolBoxText(t, running, 100); strings.Contains(text, "full output unavailable") {
		t.Fatalf("a running call claimed a missing result:\n%s", text)
	}

	done := newToolBox(newTheme("dark"), "call-17", "read", "read", missing)
	done.SetStatus("completed", "", 0, 0)
	done.SetExpanded(true)
	if text := toolBoxText(t, done, 100); !strings.Contains(text, "full output unavailable") {
		t.Fatalf("a finished call with no result stayed silent:\n%s", text)
	}
}

// A background run returns a task id at once instead of the report, which is
// the one thing about a delegation the prompt itself does not say.
func TestSpawnAgentToolBoxNamesABackgroundRun(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-6", "spawn_agent", "other", nil)
	tb.SetArgs(`{"agent":"explore","prompt":"Find every caller","background":true}`)
	tb.SetStatus("in_progress", "", 0, 0)

	// No description and no timeout were passed, so neither is claimed: the
	// row carries the agent and the one option that was set.
	title := tui.StripTerminalSequences(tb.title())
	if title != "spawn_agent explore · background" {
		t.Fatalf("title row reads %q", title)
	}
}

// The prompt is one argument, so the ten-line cap of a result preview does
// not bound it: a single paragraph would wrap over the whole transcript.
func TestSpawnAgentToolBoxCutsALongPromptUntilExpanded(t *testing.T) {
	long := strings.Repeat("delegate this piece of work in detail. ", 60) + "END-OF-BRIEF"
	tb := newToolBox(newTheme("dark"), "call-7", "spawn_agent", "other", nil)
	tb.SetArgs(`{"agent":"general","prompt":` + mustJSONString(t, long) + `}`)
	tb.SetStatus("in_progress", "", 0, 0)

	collapsed := toolBoxText(t, tb, 100)
	if !strings.Contains(collapsed, "... (ctrl+o for the whole prompt)") {
		t.Fatalf("a cut prompt must say how to read the rest:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "END-OF-BRIEF") {
		t.Fatalf("the collapsed box still carries the end of the prompt:\n%s", collapsed)
	}

	tb.SetExpanded(true)
	expanded := toolBoxText(t, tb, 100)
	if !strings.Contains(expanded, "END-OF-BRIEF") {
		t.Fatalf("expanding did not reveal the end of the prompt:\n%s", expanded)
	}
	// Nothing has been persisted for a call still in flight, so the box must
	// not report a missing result when the operator only asked for the prompt.
	if strings.Contains(expanded, "full output unavailable") {
		t.Fatalf("expanding a running call claimed a missing result:\n%s", expanded)
	}
}

// The two caps are what keeps a delegation from taking the transcript over:
// a prompt written as a list is cut by lines, one written as a paragraph by
// characters. Both are asserted on the truncation itself, since the rendered
// box pads and wraps whatever it is given.
func TestTruncatePromptCutsOnLinesAndOnCharacters(t *testing.T) {
	lines := make([]string, 0, 14)
	for i := 0; i < 14; i++ {
		lines = append(lines, "step "+itoa(i))
	}
	text, cut := truncatePrompt(strings.Join(lines, "\n"), collapsedPreviewLines, collapsedPromptChars)
	if !cut || strings.Count(text, "\n")+1 != collapsedPreviewLines {
		t.Fatalf("a %d-line prompt collapsed to %d line(s), cut=%v", len(lines), strings.Count(text, "\n")+1, cut)
	}
	if strings.Contains(text, "step 10") {
		t.Fatalf("a line past the cap survived:\n%s", text)
	}

	paragraph := strings.Repeat("x", collapsedPromptChars+50)
	text, cut = truncatePrompt(paragraph, collapsedPreviewLines, collapsedPromptChars)
	if !cut || len([]rune(text)) != collapsedPromptChars {
		t.Fatalf("a single long line collapsed to %d runes, cut=%v", len([]rune(text)), cut)
	}

	// Exactly at the caps nothing is dropped, so a short prompt never claims
	// there is more to read.
	fits := strings.Repeat("y", collapsedPromptChars)
	if text, cut = truncatePrompt(fits, collapsedPreviewLines, collapsedPromptChars); cut || text != fits {
		t.Fatalf("a prompt at the cap was cut: cut=%v, %d runes", cut, len([]rune(text)))
	}
}

// The documentation tools say what they do rather than print their ids, and
// name the query or the page the way run_command names its command.
func TestToolBoxTitleNamesTheDocumentationTools(t *testing.T) {
	search := newToolBox(newTheme("dark"), "call-docs-1", "coddy_docs_search", "read", nil)
	if title := tui.StripTerminalSequences(search.title()); title != "Searching the docs" {
		t.Fatalf("before its arguments the search box reads %q", title)
	}
	// A query is model-supplied: folded onto the title's one row.
	search.SetArgs(mustJSONObject(t, map[string]string{"query": "telegram\nproxy"}))
	if title := tui.StripTerminalSequences(search.title()); title != "Searching the docs telegram proxy" {
		t.Fatalf("search title = %q", title)
	}

	read := newToolBox(newTheme("dark"), "call-docs-2", "coddy_docs_read", "read", nil)
	read.SetArgs(mustJSONObject(t, map[string]string{"page": "features/mentions#completion"}))
	if title := tui.StripTerminalSequences(read.title()); title != "Reading the docs features/mentions#completion" {
		t.Fatalf("read title = %q", title)
	}
	contents := newToolBox(newTheme("dark"), "call-docs-3", "coddy_docs_read", "read", nil)
	contents.SetArgs("{}")
	if title := tui.StripTerminalSequences(contents.title()); title != "Reading the docs" {
		t.Fatalf("the contents read = %q", title)
	}
}

// Everything the title carries is model-supplied. SanitizeText keeps newlines
// and tabs, so a name written with one would split the row the title gets.
func TestToolBoxTitleKeepsModelNamesOnOneRow(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-13", "spawn_agent", "other", nil)
	tb.SetArgs(mustJSONObject(t, map[string]string{
		"agent": "gen\neral", "description": "review\tthe\ndiff", "prompt": "go",
	}))
	title := tui.StripTerminalSequences(tb.title())
	if strings.ContainsAny(title, "\n\t") {
		t.Fatalf("the title spans more than one row: %q", title)
	}
	if !strings.Contains(title, "gen eral") || !strings.Contains(title, "review the diff") {
		t.Fatalf("folding the name lost it: %q", title)
	}

	skill := newToolBox(newTheme("dark"), "call-14", "load_skill", "other", nil)
	skill.SetArgs(mustJSONObject(t, map[string]string{"name": "code\nreview"}))
	if title := tui.StripTerminalSequences(skill.title()); strings.ContainsAny(title, "\n\t") {
		t.Fatalf("the skill title spans more than one row: %q", title)
	}
}

// Arguments arrive as a stream, so the box renders the plain tool name until
// the agent field is there, rather than a half-filled card.
func TestSpawnAgentToolBoxFallsBackWhileArgumentsAreIncomplete(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-8", "spawn_agent", "other", nil)
	tb.SetArgs(`{"agent":"gen`)

	text := toolBoxText(t, tb, 100)
	if !strings.Contains(text, "spawn_agent") {
		t.Fatalf("the tool name is missing:\n%s", text)
	}
	if strings.Contains(text, "gen\n") || strings.Contains(text, "spawn_agent gen") {
		t.Fatalf("a truncated argument reached the title:\n%s", text)
	}
}

// load_skill pulls a whole instruction file into the turn. Which file that
// was is the one thing the operator needs from the row, exactly as an
// apply_patch box names the file it edits.
func TestLoadSkillToolBoxNamesTheSkill(t *testing.T) {
	tb := newToolBox(newTheme("dark"), "call-9", "load_skill", "other", nil)
	tb.SetArgs(`{"name":"code-review"}`)
	if text := toolBoxText(t, tb, 100); !strings.Contains(text, "load_skill code-review") {
		t.Fatalf("the skill name is missing from the box:\n%s", text)
	}

	// The catalog spells a command with a leading slash and models copy it;
	// the row names the skill either way, and an "Arguments:" envelope is
	// stripped like everywhere else the console reads a call's arguments.
	slashed := newToolBox(newTheme("dark"), "call-10", "load_skill", "other", nil)
	slashed.SetArgs(`Arguments: {"name":"/rpa-feat"}`)
	if text := toolBoxText(t, slashed, 100); !strings.Contains(text, "load_skill rpa-feat") {
		t.Fatalf("a slashed catalog name lost its skill:\n%s", text)
	}
}

// Nothing bounds a name or a task label on the way in: the model writes them
// and the call may not even be valid. An overlong one must not push the title
// over several rows of the transcript.
func TestToolBoxTitleCapsModelSuppliedNames(t *testing.T) {
	long := strings.Repeat("x", 400)
	spawn := newToolBox(newTheme("dark"), "call-11", "spawn_agent", "other", nil)
	spawn.SetArgs(`{"agent":` + mustJSONString(t, long) + `,"description":` + mustJSONString(t, long) + `,"prompt":"go"}`)
	skill := newToolBox(newTheme("dark"), "call-12", "load_skill", "other", nil)
	skill.SetArgs(`{"name":` + mustJSONString(t, long) + `}`)

	for _, tb := range []*toolBox{spawn, skill} {
		title := tui.StripTerminalSequences(tb.title())
		if len(title) > 4*maxTitleFieldChars {
			t.Fatalf("%s title is %d chars long: %q", tb.name, len(title), title)
		}
		if !strings.Contains(title, "…") {
			t.Fatalf("%s title did not mark the cut: %q", tb.name, title)
		}
	}
}

// mustJSONObject encodes call arguments as the JSON object the console
// receives, so a test never hand-escapes a payload.
func mustJSONObject(t *testing.T, args map[string]string) string {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// mustJSONString encodes a Go string as a JSON string literal, so a test can
// build an argument payload without hand-escaping it.
func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// A console session keeps every past turn in the component tree, so a
// keystroke re-renders the whole transcript. The work a frame does must follow
// what changed and not how long the session has been running: before the
// render caches this cost 287 ms per keystroke on a real 425-message bundle.
func TestKeystrokeWorkDoesNotGrowWithTheTranscript(t *testing.T) {
	frameAllocs := func(turns int) float64 {
		a := newTestApp(t)
		for i := 0; i < turns; i++ {
			a.chat.AddChild(newUserMessage(a.theme, "prompt "+itoa(i)))
			am := newAssistantMessage(a.theme, a.mdTheme, true)
			am.AppendText("Answer " + itoa(i) + "\n\nWith **markdown** and `code`.\n")
			a.chat.AddChild(am)
			tb := newToolBox(a.theme, "call_"+itoa(i), "run_command", "other", nil)
			tb.SetArgs(`{"command":"ls -la"}`)
			tb.SetStatus("completed", "total 0\ndrwxr-xr-x 2 user user 4096 .\n", 0, 0)
			a.chat.AddChild(tb)
		}
		term := &bddTerminal{cols: 120, rows: 40}
		screen := tui.NewMainScreen(term)
		screen.Root.AddChild(a.chat)
		editor := tui.NewEditor(term, tui.EditorTheme{}, 1)
		screen.Root.AddChild(editor)
		screen.SetFocus(editor)
		screen.RenderNow()
		return testing.AllocsPerRun(20, func() { screen.HandleInput([]byte("a")) })
	}

	short := frameAllocs(10)
	long := frameAllocs(200)
	t.Logf("allocations per keystroke: 10 turns=%.0f  200 turns=%.0f", short, long)
	if long > short*2+50 {
		t.Fatalf("keystroke over a long transcript allocates %.0f vs %.0f over a short one: the backlog is re-rendered on every key", long, short)
	}
}
