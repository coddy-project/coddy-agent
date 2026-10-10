package agent

// Per-model tool restrictions: models[].tools and models[].disallowed_tools.
//
// A model row may narrow the tools a session offers while it runs on that
// model. The point is context, not permission: a local model with a 49k window
// that the work only ever asks for a dozen tools should not be sent the
// schemas of the other forty (about 12k tokens), while a Codex or GPT model on
// the same configuration keeps every tool.
//
// The lists only narrow. They are applied last, to what the mode, the
// session's switches and a subagent's definition already offer, with the rule
// a subagent definition's own lists follow (subagents.AllowsTool), and they are
// read for the session's current model every time the tool set is built, so a
// switch_model, a /model or a model chosen for a spawned child changes the set
// the next request carries. Nothing is forced in: the loop works without any
// tool, so a list that leaves nothing simply offers none.
//
// The same check runs when a call is about to execute (toolCallRefused), like
// the mode's, so a call replayed from history or invented by the model for a
// tool it was not offered is refused with a clear result before any permission
// prompt.

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// modelToolEntry is the models[] row whose tool lists apply to this agent
// right now: the one of the session's current model, nil when it has none or
// none of the model's rows restricts anything. A system child (the memory
// subagent) is out of reach of them: its tool set is fixed by the runtime that
// launched it, and an allowlist written for the main loop would strip it.
func (a *Agent) modelToolEntry() *config.ModelEntry {
	if a == nil || a.cfg == nil || a.state == nil {
		return nil
	}
	if a.subagent != nil && a.subagent.Kind != "" {
		return nil
	}
	entry := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	if entry == nil || (len(entry.Tools) == 0 && len(entry.DisallowedTools) == 0) {
		return nil
	}
	return entry
}

// modelToolsAllow reports whether a models[] row offers a tool. A nil row, and
// a row with neither list, offers everything.
func modelToolsAllow(entry *config.ModelEntry, name string) bool {
	if entry == nil {
		return true
	}
	return subagents.AllowsTool(entry.Tools, entry.DisallowedTools, name)
}

// filterToolDefsForModel keeps the definitions a models[] row offers. Without
// a restriction it returns defs itself, so a configuration that sets none sends
// the bytes it always did.
func filterToolDefsForModel(defs []llm.ToolDefinition, entry *config.ModelEntry) []llm.ToolDefinition {
	if entry == nil {
		return defs
	}
	out := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if modelToolsAllow(entry, d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// sameToolNames reports whether two tool sets name the same tools in the same
// order, the test for whether a model switch moved the set a request carries.
func sameToolNames(a, b []llm.ToolDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

// narrowToolNamesForModel is filterToolDefsForModel for a list of names: the
// effective set of a spawned child, narrowed by the model it will run on.
func narrowToolNamesForModel(names []string, entry *config.ModelEntry) []string {
	if entry == nil || (len(entry.Tools) == 0 && len(entry.DisallowedTools) == 0) {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if modelToolsAllow(entry, n) {
			out = append(out, n)
		}
	}
	return out
}

// modelHidesTool reports whether the session's current model is not offered a
// tool, for the parts of the system prompt that describe one.
func (a *Agent) modelHidesTool(name string) bool {
	return !modelToolsAllow(a.modelToolEntry(), name)
}

// toolCallRefusedByModel reports whether a call must be refused at execution
// time because the session's model is not offered the tool, with the refusal
// text returned as the tool result. Definition filtering already hides the
// tool from the model, but a call can still name it (replayed from history
// recorded on another model, or made up), so the lists are checked again where
// the mode's are.
func (a *Agent) toolCallRefusedByModel(name string) (string, bool) {
	entry := a.modelToolEntry()
	if modelToolsAllow(entry, name) {
		return "", false
	}
	return fmt.Sprintf("error: tool %q is not available on model %s: its tools and disallowed_tools lists in the configuration do not offer it. Use one of the tools you were given.",
		name, entry.Model), true
}

// toolCallRefused is the execution-time gate every call passes before a
// permission prompt: the mode's allowlist first, then the model's lists. Both
// the first run of a call and its resume after a permission answer go through
// it.
func (a *Agent) toolCallRefused(mode, name string) (string, bool) {
	if refusal, refused := toolCallRefusedByMode(mode, name); refused {
		return refusal, true
	}
	return a.toolCallRefusedByModel(name)
}
