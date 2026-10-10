package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The supervisor judges evidence, not claims. The digest it reads is built by
// the harness from what the turns actually did: every tool call with its
// arguments and a slice of its result, the supervisor's own turns, and red
// flags a check would otherwise miss. The worker's final message comes last,
// labelled as claims.

const (
	// goalDigestBudget bounds the activity part of the digest; the oldest
	// steps go first when the goal's turns produced more.
	goalDigestBudget = 24000
	goalDigestArgs   = 300
	goalDigestResult = 700
	goalDigestText   = 1500
	goalDigestClaims = 4000
)

// writeTools change the workspace; their paths feed the red flags.
var writeTools = map[string]bool{"write": true, "edit": true, "apply_patch": true, "rm": true, "mv": true, "rmdir": true}

// testPathPattern matches files a change that games a check tends to touch:
// tests, test data and CI configuration.
var testPathPattern = regexp.MustCompile(`(?i)(^|/)(tests?|__tests__|spec|testdata|\.github)/|_test\.go$|(^|/)test_[^/]*\.py$|_test\.py$|\.(test|spec)\.[jt]sx?$|(^|/)conftest\.py$`)

// skipMarkers are changes that switch a check off rather than satisfy it.
var skipMarkers = regexp.MustCompile(`t\.Skip\(|@pytest\.mark\.skip|\.skip\(|xit\(|@Disabled|//\s*nolint|#\s*noqa|exit 0\b`)

// buildGoalDigest renders the evidence of the goal's turns. For the goal of a
// session that is everything since it was set; for the implicit goal of
// supervisor.enable, everything since the latest request.
func buildGoalDigest(msgs []llm.Message, goal GoalState, implicit bool) string {
	start := goalStartIndex(msgs, goal, implicit)
	window := msgs[start:]
	var steps []string
	var claims string
	results := toolResults(window)
	changed := map[string]bool{}
	var flags []string
	for i, m := range window {
		switch m.Role {
		case llm.RoleUser:
			if m.GoalTurn != nil {
				steps = append(steps, "[supervisor] "+GoalTurnNote(GoalTurnUpdate(m.GoalTurn)))
				continue
			}
			if m.BackgroundWake != nil {
				steps = append(steps, "[background] "+BackgroundWakeNote(BackgroundWakeUpdate(m.BackgroundWake)))
				continue
			}
			if m.CompactionSummary {
				// Written by the worker's own model when the history was
				// folded: its account of the work, not the operator's words.
				// A row written inside a turn starts with the request (and the
				// follow-ups sent during it) verbatim, which is the operator's
				// text, and only what follows it is the worker's account. Each
				// part is clipped on its own: a long request must not leave
				// the summary out, nor the summary the request.
				if prompt, summary, ok := SplitInTurnSummary(m.Content); ok {
					if text := strings.TrimSpace(UserMessageDisplayText(prompt)); text != "" {
						steps = append(steps, "[operator] "+clipRunes(text, goalDigestText))
					}
					steps = append(steps, "[summary of earlier steps of this request, written by the worker: claims, not evidence] "+clipRunes(strings.TrimSpace(summary), goalDigestText))
					continue
				}
				steps = append(steps, "[summary of earlier turns, written by the worker: claims, not evidence] "+clipRunes(strings.TrimSpace(m.Content), goalDigestText))
				continue
			}
			if text := strings.TrimSpace(UserMessageDisplayText(m.Content)); text != "" {
				steps = append(steps, "[operator] "+clipRunes(text, goalDigestText))
			}
		case llm.RoleAssistant:
			text := strings.TrimSpace(m.Content)
			if len(m.ToolCalls) == 0 && text != "" && lastAssistantText(window[i+1:]) == "" {
				claims = text
			} else if text != "" {
				steps = append(steps, "[assistant] "+clipRunes(text, goalDigestText))
			}
			for _, call := range m.ToolCalls {
				steps = append(steps, toolStep(call, results[call.ID]))
				if writeTools[call.Name] {
					for _, p := range callPaths(call.InputJSON) {
						changed[p] = true
					}
					if skipMarkers.MatchString(call.InputJSON) {
						flags = append(flags, fmt.Sprintf("a %s call adds a skip or suppression marker: %s", call.Name, clipRunes(compactSpace(call.InputJSON), 200)))
					}
				}
			}
		}
	}
	var testFiles []string
	for p := range changed {
		if testPathPattern.MatchString(filepath.ToSlash(p)) {
			testFiles = append(testFiles, p)
		}
	}
	sort.Strings(testFiles)
	if len(testFiles) > 0 {
		flags = append([]string{"tests, test data or CI files were changed: " + strings.Join(testFiles, ", ") + " - check that the change serves the objective rather than loosening a check"}, flags...)
	}

	var b strings.Builder
	omitted := 0
	total := 0
	keep := len(steps)
	for keep > 0 && total+len(steps[keep-1]) <= goalDigestBudget {
		total += len(steps[keep-1]) + 1
		keep--
	}
	omitted = keep
	b.WriteString("## Activity since the goal was set (oldest first)\n")
	if omitted > 0 {
		fmt.Fprintf(&b, "[%d earlier steps omitted to fit; judge only from what is shown, and treat anything you cannot see as unverified]\n", omitted)
	}
	if len(steps) == 0 {
		b.WriteString("(no activity)\n")
	}
	for _, s := range steps[omitted:] {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if len(changed) > 0 {
		paths := make([]string, 0, len(changed))
		for p := range changed {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		b.WriteString("\n## Files the turns changed\n")
		b.WriteString(strings.Join(paths, "\n"))
		b.WriteString("\n")
	}
	if len(flags) > 0 {
		b.WriteString("\n## Red flags\n")
		for _, f := range flags {
			b.WriteString("- " + f + "\n")
		}
	}
	b.WriteString("\n## The worker's final message (claims, not evidence)\n")
	if claims == "" {
		b.WriteString("(the last turn ended without a final message)\n")
	} else {
		b.WriteString(clipRunes(claims, goalDigestClaims))
		b.WriteString("\n")
	}
	return b.String()
}

// goalStartIndex is where the goal's turns begin in msgs.
func goalStartIndex(msgs []llm.Message, goal GoalState, implicit bool) int {
	if implicit {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == llm.RoleUser && msgs[i].GoalTurn == nil && msgs[i].BackgroundWake == nil {
				return i
			}
		}
		return 0
	}
	// The kickoff of this goal opens its turns; without one (a goal set
	// through the API before a plain prompt), the time it was set does.
	for i := len(msgs) - 1; i >= 0; i-- {
		if t := msgs[i].GoalTurn; t != nil && t.Kind == "kickoff" && t.Objective == goal.Objective {
			return i
		}
	}
	setAt, err := time.Parse(time.RFC3339Nano, goal.SetAt)
	if err != nil {
		return 0
	}
	for i, m := range msgs {
		at, err := time.Parse(time.RFC3339, m.CreatedAt)
		if err == nil && !at.Before(setAt.Truncate(time.Second)) {
			return i
		}
	}
	return len(msgs)
}

func lastAssistantText(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

func toolResults(msgs []llm.Message) map[string]string {
	out := make(map[string]string)
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolCallID != "" {
			out[m.ToolCallID] = m.Content
		}
	}
	return out
}

// toolStep is one tool call as the check reads it: the call, then the head
// and the tail of its result, where errors and exit codes are.
func toolStep(call llm.ToolCall, result string) string {
	line := fmt.Sprintf("[tool] %s %s", call.Name, clipRunes(compactSpace(call.InputJSON), goalDigestArgs))
	result = strings.TrimSpace(result)
	if result == "" {
		return line + "\n  -> (no result recorded)"
	}
	return line + "\n  -> " + strings.ReplaceAll(headTail(result, goalDigestResult), "\n", "\n     ")
}

func headTail(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	half := maxRunes / 2
	return string(r[:half]) + "\n[...]\n" + string(r[len(r)-half:])
}

func clipRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}

func compactSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// callPaths pulls the file paths out of a write tool's arguments.
func callPaths(input string) []string {
	var out []string
	for _, key := range []string{"path", "file_path", "source", "destination", "target"} {
		re := regexp.MustCompile(`"` + key + `"\s*:\s*"([^"]+)"`)
		for _, m := range re.FindAllStringSubmatch(input, -1) {
			out = append(out, m[1])
		}
	}
	// apply_patch names its files in the patch headers.
	for _, m := range patchHeader.FindAllStringSubmatch(input, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

var patchHeader = regexp.MustCompile(`\*\*\* (?:Add|Update|Delete) File: ([^\\\n]+)`)
