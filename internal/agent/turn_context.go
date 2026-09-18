package agent

// The turn context block: everything the model needs that moves while a turn
// runs, carried after the replayed history instead of inside the system prompt.
//
// A provider caches a request by its prefix. The system message is messages[0],
// so a byte that moves there throws away the cached copy of the whole
// conversation behind it - the wall clock alone did that on every single
// request. Keeping the system message frozen for the turn and appending what
// moved turns the cache miss into the last few hundred tokens of the request.

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

const (
	turnContextOpenTag  = "<turn_context>"
	turnContextCloseTag = "</turn_context>"
	// turnContextPreamble says who wrote the block, so the model does not read
	// it as the user asking for something.
	turnContextPreamble = "Runtime state refreshed by Coddy for this step. It is not a message from the user; do not answer it, just take it into account."
)

// buildTurnContext renders the block appended after the history on every
// request of a turn: the wall clock, the live todo checklist, the rules a
// tool call activated after the system prompt was frozen, and the memory
// subagent's report for this turn. A volatile template under prompts.dir
// prints the clock and the checklist into the system message itself and is
// re-rendered per step, so its block carries the memory report alone, and
// nothing at all when there is none; the caller then sends the history alone.
func (a *Agent) buildTurnContext(frozen *systemPromptBuild) string {
	if frozen != nil && frozen.Volatile {
		// No template prints these two, so a volatile one gets them here too.
		var sections []string
		for _, section := range []string{a.memoryTurnContextSection(), a.backgroundTasksSection()} {
			if section != "" {
				sections = append(sections, section)
			}
		}
		if len(sections) == 0 {
			return ""
		}
		return turnContextOpenTag + "\n" + turnContextPreamble + "\n\n" + strings.Join(sections, "\n\n") + "\n" + turnContextCloseTag
	}
	var parts []string
	parts = append(parts, "## Current UTC time\n\n"+a.turnClock(frozen).Format(time.RFC3339))

	// Agent mode only, matching the built-in templates: the todo tools are not
	// offered in plan or ask mode, and a checklist left over from an earlier
	// agent turn was never shown there.
	if frozen != nil && frozen.Mode == "agent" {
		if todo := checklistMarkdownFromPlan(a.state.GetPlan()); todo != "" {
			parts = append(parts, "## Current todo checklist\n\n"+todo)
		}
	}

	if section := a.activatedRulesSection(frozen); section != "" {
		parts = append(parts, section)
	}

	// The memory subagent's report for this turn (memory_run.go). It rides
	// here on every step and never in the system message, so a recall that
	// differs from the previous turn's does not move the cached prefix.
	if section := a.memoryTurnContextSection(); section != "" {
		parts = append(parts, section)
	}

	if section := a.backgroundTasksSection(); section != "" {
		parts = append(parts, section)
	}

	return turnContextOpenTag + "\n" + turnContextPreamble + "\n\n" +
		strings.Join(parts, "\n\n") + "\n" + turnContextCloseTag
}

// backgroundTasksSection lists the background tasks of this session that are still
// running, the way background_list shows them. A model starts a task, keeps working,
// and several steps later has to remember that the task exists - and that a server it
// started is still up when it writes its summary. Saying it on every request costs a
// line per task and saves the background_list call that would otherwise be the only
// way to find out. System tasks (the memory run of the turn) are the runtime's own
// errands and are left out, as they are from background_list; with background runs
// switched off there are no tools to act on a task, so nothing is said.
func (a *Agent) backgroundTasksSection() string {
	if a.cfg == nil || !a.cfg.Tools.Background.ResolvedEnabled() || a.state == nil {
		return ""
	}
	now := a.now()
	var lines []string
	for _, task := range bgtask.Default().List(a.state.GetID()) {
		if task.Status.Finished() || task.SystemTask() {
			continue
		}
		lines = append(lines, "- "+shell.FormatBackgroundTask(task, now))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Background tasks\n\nStill running in this session. Read one with `background_output`, end one with `background_stop`, " +
		"and tell the user about any you leave running.\n\n" + strings.Join(lines, "\n")
}

// activatedRulesSection renders the rules that became active after frozen was
// built - a glob rule or a nested AGENTS.md that a filesystem tool call reached
// mid-turn. They are not folded into the system prompt, which stays as the turn
// started it; the next turn's prompt picks them up from the sticky set.
func (a *Agent) activatedRulesSection(frozen *systemPromptBuild) string {
	if frozen == nil || !frozen.RendersRules {
		return ""
	}
	rs, ok := a.state.(rulesState)
	if !ok {
		return ""
	}
	added := rules.Added(frozen.RenderedRules, rs.GetActiveAutoRules())
	return rules.RenderSection("## Project rules activated by this turn", added)
}

// turnClock is the reading this turn was stamped with when its system prompt
// was rendered. It does not tick between the steps of a turn on purpose: the
// lane re-issues a step that produced nothing, and that replay has to be the
// request that failed, byte for byte. A turn long enough for the difference to
// matter can ask the shell.
func (a *Agent) turnClock(frozen *systemPromptBuild) time.Time {
	if frozen != nil && !frozen.Clock.IsZero() {
		return frozen.Clock
	}
	return a.now().UTC()
}

// now is the agent's clock, overridable in tests that assert on a rendered
// timestamp.
func (a *Agent) now() time.Time {
	if a != nil && a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// withTurnContext returns the message list to send: the projection plus the
// turn context as a trailing user message. It never writes into the caller's
// backing array, so the working slice the loop keeps appending to is untouched,
// and the block is never persisted to the transcript.
func withTurnContext(msgs []llm.Message, block string) []llm.Message {
	if strings.TrimSpace(block) == "" {
		return msgs
	}
	out := make([]llm.Message, len(msgs), len(msgs)+1)
	copy(out, msgs)
	return append(out, llm.Message{Role: llm.RoleUser, Content: block})
}
