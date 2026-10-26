package session

import (
	"fmt"
	"regexp"
	"strings"
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

// buildGoalDigest renders the evidence of the goal's turns (goalRows), with
// what the migration rule found after the latest of them. The red flags are
// the findings of every rule over all of the goal's rows, so a flag stays when
// the step that raised it no longer fits the budget.
func buildGoalDigest(msgs []llm.Message, goal GoalState, implicit bool, files goalFiles) string {
	rows := goalRows(msgs, goal, implicit)
	last := -1
	for _, i := range rows {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			last = i
		}
	}
	var steps []string
	var claims string
	for _, i := range rows {
		m := msgs[i]
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
				// A row written inside a turn starts with the message the turn
				// opened with, verbatim: the text of whoever wrote that message,
				// not the worker's. Only what follows it is the worker's account.
				// Each part is clipped on its own: a long request must not leave
				// the summary out, nor the summary the request.
				if prompt, summary, ok := SplitInTurnSummary(m.Content); ok {
					if text := strings.TrimSpace(UserMessageDisplayText(prompt)); text != "" {
						steps = append(steps, "[the message this turn started with] "+clipRunes(text, goalDigestText))
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
			if i == last && len(m.ToolCalls) == 0 {
				claims = text
			} else if text != "" {
				steps = append(steps, "[assistant] "+clipRunes(text, goalDigestText))
			}
			results := callResults(msgs, i)
			for _, call := range m.ToolCalls {
				steps = append(steps, toolStep(call, results[call.ID]))
			}
		}
	}
	changed, findings := goalFindings(msgs, rows, goal, files)
	flags := flagLines(findings, true)
	if files.unavailable != "" {
		flags = append(flags, "migration files could not be compared with the goal's start ("+files.unavailable+"); whether they changed is unknown")
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
		b.WriteString("\n## Files the turns changed\n")
		b.WriteString(strings.Join(changed, "\n"))
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

// goalRows are the indexes in msgs of the goal's turns: for the session's
// goal, the rows stored while it was active, which carry its id; for the
// implicit goal of supervisor.enable, every row from the latest request on.
func goalRows(msgs []llm.Message, goal GoalState, implicit bool) []int {
	var rows []int
	start := 0
	if implicit {
		start = latestRequestIndex(msgs)
	}
	for i := start; i < len(msgs); i++ {
		if implicit || goal.ID != "" && msgs[i].Goal == goal.ID {
			rows = append(rows, i)
		}
	}
	return rows
}

// latestRequestIndex is where the latest request begins in msgs: the last
// message the operator typed. A summary row is user-role too, and one a fold
// inside the turn inserted after the prompt is no request: skipping it finds
// the prompt itself.
func latestRequestIndex(msgs []llm.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if isRealUser(msgs[i]) && msgs[i].GoalTurn == nil && msgs[i].BackgroundWake == nil {
			return i
		}
	}
	return 0
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

// callPaths pulls the file paths out of a write tool's arguments; mv names
// its two files src and dst.
func callPaths(input string) []string {
	var out []string
	for _, key := range []string{"path", "file_path", "src", "dst", "source", "destination", "target"} {
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
