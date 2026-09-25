//go:build cli

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// Live status line shown next to the spinner while a turn runs: what phase the agent is
// in right now, and for how long.
//
// The line carries the phase and *nothing it acts on* - no path, no command, no url.
// What a step acts on is named once, by the tool box above the line, so every phrase
// here has to read as a complete phrase on its own ("Running a command", never
// "Running"). DESIGN.md, States -> Working, is the rule.
//
// The SPA carries the same phrase table in TypeScript
// (external/ui/src/ui/chat/liveStatus.ts). The two cannot share code across the language
// boundary, so a tool added to one belongs in the other as well. The console has no i18n
// (every string in header.go / footer.go is a literal), so the phrases live here directly
// instead of behind keys.

// Waiting longer than these reads as slower than usual, then as no response at all.
const (
	waitingSlowAfter  = 15 * time.Second
	waitingStuckAfter = 60 * time.Second
)

const (
	statusWaitingModel = "Waiting for the model"
	statusWaitingSlow  = "The model is taking longer than usual"
	statusWaitingStuck = "Still no response from the server"
	// statusConnectingMCP is the step of a turn sent while the session's
	// configured MCP servers are still connecting in the background: the
	// turn waits for its tool list. The SPA has no twin, since no web
	// surface defers the connect.
	statusConnectingMCP = "Connecting MCP servers"
)

// liveStatus is the current step of a running turn. counts says the step shows a clock
// of its own after the phrase. The turn's clock leads the line and covers the model's
// own phases - waiting, thinking, responding - so only a step that runs something other
// than the model (a tool call, the memory run) counts; a step blocked on the operator
// never does, because a climbing counter there would be a lie.
// step identifies the step the phrase belongs to and is never rendered: two calls in a
// row can share a phrase ("Reading a file" twice), and without it the second one would
// inherit the first one's clock.
type liveStatus struct {
	verb      string
	step      string
	startedAt time.Time
	counts    bool
	waiting   bool
}

// newWaitingStatus starts the "waiting for the model" phase, whose phrase escalates with
// the time since it began. It shows no clock of its own: before the first token the
// line is the turn clock and this phrase.
func newWaitingStatus() liveStatus {
	return liveStatus{verb: statusWaitingModel, startedAt: time.Now(), waiting: true}
}

// newModelStatus starts a phase of the model's own work - thinking, responding. The
// turn clock covers it, so it carries no second clock.
func newModelStatus(verb string) liveStatus {
	return liveStatus{verb: verb, startedAt: time.Now()}
}

// newWorkingStatus starts a step that runs something other than the model, so it shows a
// clock of its own. step is the identity of that step - the tool call id - and is never
// rendered; "" is fine where only one such step can run at a time.
func newWorkingStatus(verb, step string) liveStatus {
	return liveStatus{verb: verb, step: step, startedAt: time.Now(), counts: true}
}

// blockStatus parks the status line on an operator gate (permission or question
// modal). The phrase is kept apart from stepStatus instead of replacing it: the
// gated tool's in_progress update can land after the modal opened (updatesCh and
// permCh race in the UI select), and an overlay cannot be overwritten by it. It
// renders without a counter - nothing is running while the operator decides.
func (a *App) blockStatus(verb string) {
	a.stepBlocked = verb
}

// unblockStatus lifts the gate. The underlying step resumes now, so its clock
// restarts: time spent waiting on the operator is not work and must not count.
func (a *App) unblockStatus() {
	if a.stepBlocked == "" {
		return
	}
	a.stepBlocked = ""
	if a.turnActive {
		a.stepStatus.startedAt = time.Now()
	}
}

// statusVerbForTool is the present-progressive phrase for a backend tool id. Tool ids are
// the raw registry names; a tool an MCP server serves is named by its server and its own
// name, and anything else unknown falls back to a generic phrase.
//
// Every phrase returned here stands on its own: the status line renders it and nothing
// else, so "Running" would read as a sentence cut in half.
func statusVerbForTool(toolName string) string {
	n := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case n == "":
		return "Running a tool"
	case strings.HasPrefix(n, "coddy_todo_"):
		if strings.HasSuffix(n, "_read") {
			return "Reading the plan"
		}
		return "Updating the plan"
	case strings.HasPrefix(n, "coddy_scheduler_"):
		return "Updating the schedule"
	case strings.HasPrefix(n, "coddy_memory_"):
		return "Working with memory"
	case strings.HasPrefix(n, "config_"):
		return "Updating the configuration"
	case strings.HasPrefix(n, "background_"):
		// The background family runs long by design - background_wait alone parks for up
		// to a minute - so a generic phrase here reads as a frozen row rather than as work.
		switch n {
		case "background_wait":
			return "Waiting for a background task"
		case "background_output":
			return "Reading background output"
		case "background_stop":
			return "Stopping a background task"
		case "background_reap":
			return "Cleaning up background tasks"
		default:
			return "Checking background tasks"
		}
	}
	switch n {
	case "read":
		return "Reading a file"
	case "list_dir", "print_tree":
		return "Browsing a directory"
	case "grep", "glob":
		return "Searching in files"
	case "edit", "apply_patch":
		return "Editing a file"
	case "write":
		return "Writing a file"
	case "run_command":
		return "Running a command"
	case "ssh_run_command":
		return "Running a command over SSH"
	case "mkdir":
		return "Creating a directory"
	case "touch":
		return "Creating a file"
	case "mv":
		return "Moving a file"
	case "rm", "rmdir":
		return "Deleting a file"
	case "websearch":
		return "Searching the web"
	case "coddy_docs_search":
		return "Searching the docs"
	case "coddy_docs_read":
		return "Reading the docs"
	case "webfetch":
		return "Fetching a page"
	case "http_request":
		return "Sending a request"
	case "load_skill":
		return "Loading a skill"
	case "spawn_agent":
		return "Running a subagent"
	case "preview_server":
		return "Starting a preview server"
	case "plan_write", "plan_exit":
		return "Updating the plan"
	case "plan_read", "plan_list":
		return "Reading the plan"
	case "question":
		return "Waiting for your answer"
	}
	// Last, so a tool Coddy ships keeps its own phrase even if its id ever carries
	// the separator. The web UI decides the same way round: the catalogue first,
	// this only on the generic key.
	if phrase := mcpToolPhrase(toolName); phrase != "" {
		return phrase
	}
	return "Running a tool"
}

// statusTargetFromArgs picks the one argument that identifies what a call acts on: the
// path it reads, the command it runs, the pattern it searches for. Returns "" when the
// call takes no meaningful target or its arguments have not streamed in yet.
//
// This belongs to the tool box title (chat.go), which names what a call acts on. The
// status line does not: it carries the phase and nothing else.
func statusTargetFromArgs(toolName, argsJSON string) string {
	raw := strings.TrimSpace(argsJSON)
	// The manager streams arguments either bare or behind an "Arguments:" label.
	if rest, ok := cutArgumentsPrefix(raw); ok {
		raw = rest
	}
	if !strings.HasPrefix(raw, "{") {
		return ""
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "run_command", "ssh_run_command":
		return stringArg(args, "command")
	case "grep", "glob":
		return stringArg(args, "pattern")
	case "websearch", "coddy_docs_search":
		return stringArg(args, "query")
	case "coddy_docs_read":
		// A page of the documentation built into the binary, with its section.
		return stringArg(args, "page")
	case "http_request":
		// The method is half of what a request does; the url alone reads like a fetch.
		method, target := strings.ToUpper(stringArg(args, "method")), stringArg(args, "url")
		if method != "" && target != "" {
			return method + " " + target
		}
		return target
	case "spawn_agent":
		// The definition name is what the operator recognises; the prompt would fill the row.
		return stringArg(args, "agent")
	case "mv":
		return stringArg(args, "src")
	case "question":
		return ""
	default:
		// read / write / edit / apply_patch / mkdir / touch / rm / rmdir / print_tree /
		// plan_* take a path; webfetch takes a url.
		if target := stringArg(args, "path", "filePath", "file_path", "url", "name"); target != "" {
			return target
		}
		if _, _, ok := mcpToolNameParts(toolName); !ok {
			return ""
		}
		// An MCP server names its own arguments, so a call taking none of the above
		// would show nothing at all beside a phrase that cannot say what it does. The
		// first argument that reads as a label is what such a call is about. Only for
		// those: a Coddy tool landing here keeps naming the argument it is documented
		// to take, so a write without its path never shows the file body instead.
		return firstLabelArg(raw)
	}
}

// mcpToolNameParts reads the "<server>__<tool>" name every MCP tool joins the
// function-calling list under (internal/mcp.ToolInfo.ToLLMToolDefinition), with the
// "mcp__" prefix other agents spell the same call with accepted as well
// (internal/hooks.MatchTool). A server name can never contain "__"
// (internal/mcp.ValidateServerName), so the first separator is the split and everything
// after it is the tool's own name.
func mcpToolNameParts(toolName string) (server, tool string, ok bool) {
	const prefix = "mcp__"
	name := strings.TrimSpace(toolName)
	// Other agents spell the same call `mcp__<server>__<tool>`, so the prefix is
	// dropped - but only when what is left is still a namespaced name. A server
	// really called `mcp` reaches us as `mcp__<tool>`, and stripping there would
	// leave a bare tool name that parses as nothing.
	if len(name) > len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) &&
		strings.Contains(name[len(prefix):], "__") {
		name = name[len(prefix):]
	}
	at := strings.Index(name, "__")
	if at <= 0 {
		return "", "", false
	}
	server, tool = name[:at], name[at+2:]
	if server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// mcpToolPhrase names a call to a tool an MCP server serves the way the built-in ids
// name their action. It answers "" for anything that is not a namespaced call. The web
// UI says the same through the `tool.name.mcp` dictionary entry.
func mcpToolPhrase(toolName string) string {
	server, tool, ok := mcpToolNameParts(toolName)
	if !ok {
		return ""
	}
	return "Calling " + tool + " on the MCP server " + server
}

// Longest argument value that still reads as a label on the row rather than as a body.
const maxLabelArgChars = 120

// firstLabelArg is the first argument of a call that reads as a label: a non-empty
// single-line string short enough for the row. encoding/json decodes an object into an
// unordered map, so the order the model wrote the arguments in is recovered from the
// token stream - the leading one wins, which is where a tool puts what it acts on.
func firstLabelArg(raw string) string {
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ""
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return ""
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return ""
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			continue
		}
		if strings.ContainsAny(text, "\r\n") {
			continue
		}
		label := strings.TrimSpace(text)
		if label != "" && len([]rune(label)) <= maxLabelArgChars {
			return label
		}
	}
	return ""
}

// cutArgumentsPrefix strips a leading "Arguments:" label, case-insensitively.
func cutArgumentsPrefix(raw string) (string, bool) {
	const label = "arguments:"
	if len(raw) < len(label) || !strings.EqualFold(raw[:len(label)], label) {
		return raw, false
	}
	return strings.TrimSpace(raw[len(label):]), true
}

func stringArg(args map[string]interface{}, names ...string) string {
	for _, name := range names {
		if value, ok := args[name].(string); ok {
			return value
		}
	}
	return ""
}

// truncateRunes cuts value to at most max runes, marking the cut with an ellipsis.
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	keep := max - 1
	if keep < 1 {
		keep = 1
	}
	return string(runes[:keep]) + "…"
}

// formatElapsed renders a duration as whole seconds: 0s, 59s, 1m 05s, 59m 59s, 1h 00m.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		return ""
	}
	total := int(d / time.Second)
	if total < 60 {
		return itoa(total) + "s"
	}
	minutes := total / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %02ds", minutes, total%60)
	}
	return fmt.Sprintf("%dh %02dm", minutes/60, minutes%60)
}

// statusText renders one status as it appears next to the spinner. elapsed is passed in
// so the pure formatting stays testable without a clock.
func (s liveStatus) statusText(elapsed time.Duration) string {
	verb := s.verb
	if s.waiting {
		switch {
		case elapsed >= waitingStuckAfter:
			verb = statusWaitingStuck
		case elapsed >= waitingSlowAfter:
			verb = statusWaitingSlow
		}
	}
	// The phrase and its clock, nothing else: what the step acts on is named by the
	// tool box above the line, once.
	var b strings.Builder
	b.WriteString(verb)
	if s.counts {
		if formatted := formatElapsed(elapsed); formatted != "" {
			b.WriteString(" · ")
			b.WriteString(formatted)
		}
	}
	return b.String()
}

// turnLine renders the turn's own numbers, which lead the status line: how long the
// turn has been running, how many tokens the model has generated in it, how many
// background tasks run right now. Tokens and tasks appear once there are any, so a turn
// that has not heard from the model reads as its clock alone. The web UI renders the
// same line (external/ui/src/ui/messages/TypingDotsMessage.tsx).
func turnLine(elapsed time.Duration, tokens, runningTasks int) string {
	parts := []string{formatElapsed(elapsed)}
	if tokens > 0 {
		parts = append(parts, tui.FormatTokenCount(tokens)+" "+plural(tokens, "token", "tokens"))
	}
	if runningTasks > 0 {
		parts = append(parts, itoa(runningTasks)+" "+plural(runningTasks, "running task", "running tasks"))
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// statusMessage is the loader's message provider: it runs inside Loader.Render on the UI
// goroutine, so reading App state here needs no extra synchronization.
func (a *App) statusMessage() string {
	step := a.stepMessage()
	if a.turnStartedAt.IsZero() {
		return step
	}
	return turnLine(time.Since(a.turnStartedAt), a.turnTokens, a.runningTasks) + " · " + step
}

// stepMessage is what the turn is doing right now, without the turn's own numbers.
func (a *App) stepMessage() string {
	if a.stepBlocked != "" {
		return a.stepBlocked
	}
	if a.stepStatus.verb == "" {
		return statusWaitingModel
	}
	elapsed := time.Duration(0)
	if !a.stepStatus.startedAt.IsZero() {
		elapsed = time.Since(a.stepStatus.startedAt)
	}
	return a.stepStatus.statusText(elapsed)
}

// applyTurnProgress takes the server's account of the running turn. The token count
// is the agent's: provider figures for the calls that finished, an estimate for the
// one in flight. The clock is the console's own for a turn it started - it knows when
// the operator pressed enter - and the server's for a turn it only attached to.
func (a *App) applyTurnProgress(u acp.TurnProgressUpdate) {
	a.turnTokens = u.OutputTokens
	if a.turnStartedAt.IsZero() && u.ElapsedMs >= 0 {
		a.turnStartedAt = time.Now().Add(-time.Duration(u.ElapsedMs) * time.Millisecond)
	}
}

// setStatus replaces the current step. A repeat of the same phrase for the same step
// keeps its start time so the counter does not restart on every streamed chunk; the
// next call restarts it even when it reads the same, because its step differs.
func (a *App) setStatus(next liveStatus) {
	if a.stepStatus.verb == next.verb && a.stepStatus.step == next.step {
		return
	}
	a.stepStatus = next
}
