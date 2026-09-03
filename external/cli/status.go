//go:build cli

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Live status line shown next to the spinner while a turn runs: what the agent is doing
// right now, what it is doing it to, and for how long.
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

// Longest target rendered inline before the middle of a path or the tail of a command is
// dropped.
const maxStatusTargetChars = 48

const (
	statusWaitingModel = "Waiting for the model"
	statusWaitingSlow  = "The model is taking longer than usual"
	statusWaitingStuck = "Still no response from the server"
)

// liveStatus is the current step of a running turn. counts is false for steps that are
// blocked on the operator, where a climbing counter would be a lie.
type liveStatus struct {
	verb      string
	target    string
	startedAt time.Time
	counts    bool
	waiting   bool
}

// newWaitingStatus starts the "waiting for the model" phase, whose phrase escalates with
// elapsed time.
func newWaitingStatus() liveStatus {
	return liveStatus{verb: statusWaitingModel, startedAt: time.Now(), counts: true, waiting: true}
}

// newWorkingStatus starts a step that names what it is doing.
func newWorkingStatus(verb, target string) liveStatus {
	return liveStatus{verb: verb, target: target, startedAt: time.Now(), counts: true}
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
// the raw registry names; unknown ones - MCP tools included - fall back to a generic
// phrase and keep their id as the target so the row stays debuggable.
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
		return "Reading"
	case "list_dir", "print_tree":
		return "Listing"
	case "grep", "glob":
		return "Searching"
	case "edit", "apply_patch":
		return "Editing"
	case "write":
		return "Writing"
	case "run_command":
		return "Running"
	case "ssh_run_command":
		return "Running over SSH"
	case "mkdir":
		return "Creating directory"
	case "touch":
		return "Creating file"
	case "mv":
		return "Moving"
	case "rm", "rmdir":
		return "Deleting"
	case "websearch":
		return "Searching the web"
	case "webfetch":
		return "Fetching"
	case "load_skill":
		return "Loading a skill"
	case "plan_write", "plan_exit":
		return "Updating the plan"
	case "plan_read", "plan_list":
		return "Reading the plan"
	case "question":
		return "Waiting for your answer"
	default:
		return "Running a tool"
	}
}

// statusTargetFromArgs picks the one argument that identifies what a call acts on: the
// path it reads, the command it runs, the pattern it searches for. Returns "" when the
// call takes no meaningful target or its arguments have not streamed in yet.
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
	case "websearch":
		return stringArg(args, "query")
	case "mv":
		return stringArg(args, "src")
	case "question":
		return ""
	default:
		// read / write / edit / apply_patch / mkdir / touch / rm / rmdir / print_tree /
		// plan_* take a path; webfetch takes a url.
		return stringArg(args, "path", "filePath", "file_path", "url", "name")
	}
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

// truncateStatusTarget shortens a target for the single-line status row. Paths lose
// leading segments (the tail identifies the file); everything else loses its tail (the
// leading program name identifies a command).
func truncateStatusTarget(raw string, max int) string {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if collapsed == "" || max <= 0 {
		return ""
	}
	if !looksLikePath(collapsed) {
		return truncateRunes(collapsed, max)
	}
	segments := make([]string, 0, 8)
	for _, s := range strings.FieldsFunc(collapsed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if s != "" {
			segments = append(segments, s)
		}
	}
	// Display separators are always "/" so a Windows path reads the same as a POSIX one.
	value := strings.Join(segments, "/")
	if len([]rune(value)) <= max {
		return value
	}
	last := value
	if n := len(segments); n > 0 {
		last = segments[n-1]
	}
	if len([]rune(last))+2 > max {
		return "…/" + truncateRunes(last, max-2)
	}
	tail := last
	for i := len(segments) - 2; i >= 0; i-- {
		next := segments[i] + "/" + tail
		if len([]rune(next))+2 > max {
			break
		}
		tail = next
	}
	return "…/" + tail
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

// looksLikePath reports whether a target is path-shaped: it has a separator and no spaces.
func looksLikePath(value string) bool {
	return (strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\')) &&
		!strings.ContainsRune(value, ' ')
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
	var b strings.Builder
	b.WriteString(verb)
	if target := truncateStatusTarget(s.target, maxStatusTargetChars); target != "" {
		b.WriteString(" ")
		b.WriteString(target)
	}
	if s.counts {
		if formatted := formatElapsed(elapsed); formatted != "" {
			b.WriteString(" · ")
			b.WriteString(formatted)
		}
	}
	return b.String()
}

// statusMessage is the loader's message provider: it runs inside Loader.Render on the UI
// goroutine, so reading App state here needs no extra synchronization.
func (a *App) statusMessage() string {
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

// setStatus replaces the current step. A repeat of the same verb and target keeps its
// start time so the counter does not restart on every streamed chunk.
func (a *App) setStatus(next liveStatus) {
	if a.stepStatus.verb == next.verb && a.stepStatus.target == next.target {
		return
	}
	a.stepStatus = next
}
