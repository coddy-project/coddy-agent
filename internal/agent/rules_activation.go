package agent

// Rules scoped to paths reach the model with whatever brought their path into
// play - the result of the tool call that touched it, or the user message that
// mentioned it (mentions.go) - and never through the system prompt. A provider
// caches a request by its prefix, and the system message opens every request:
// a rule written into it mid-session would throw away the cached copy of the
// whole conversation behind it, once per rule that activates. Appended where
// the conversation already grows, it costs its own length once.

import (
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
)

// toolCallRulesLead opens the rules a tool call brought in, so the model reads
// them as standing instructions rather than as part of the output above.
const toolCallRulesLead = "Project rules for the path this call touched; follow them from here on:"

// toolCallRules renders the rules a tool call brings into play for the first
// time, to travel with its result (llm.Message.Rules): a glob rule (Cursor
// globs, Claude Code paths) whose pattern matches a file the call targets, and
// the nested AGENTS.md and DESIGN.md files on the chain of folders down to every
// path it targets, read at this moment and never before - a session looks at a
// folder only when a tool enters it. Callers read it before the call runs, on
// the arguments the model wrote (or the approved ones, when a permission answer
// resumes the call). A rule the model can already read, attached to an earlier
// result or message it is still sent, is left out; one a compaction folded away
// comes back with the next call that matches it. It is the empty string when
// there is nothing new.
func (a *Agent) toolCallRules(mode string, tc llm.ToolCall, cwd string) string {
	rs, ok := a.state.(rulesState)
	if !ok {
		return ""
	}
	paths := toolfs.ToolCallPaths(tc.Name, tc.InputJSON, cwd)
	if len(paths) == 0 {
		return ""
	}
	matched := rules.MatchScoped(rs.GetRulesCatalog(), withoutDirectories(paths))
	if a.agentsOnDemand() {
		matched = append(matched, rules.AgentsForPaths(cwd, paths, nil)...)
	}
	if len(matched) == 0 || !a.rulesRendered(mode) {
		return ""
	}
	home := a.homeDir()
	fresh := freshRules(deliveredRulePaths(rs.GetMessages()), cwd, home, matched)
	if len(fresh) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(toolCallRulesLead)
	for _, r := range fresh {
		b.WriteString("\n\n")
		b.WriteString(resourceAttachmentXML(session.RuleAttachment(cwd, home, r)))
	}
	return b.String()
}

// withoutDirectories drops the paths that name an existing directory. A glob
// is written for files (**/*.go, external/ui/**/*), and a folder a call lists
// or searches would either match a pattern meant for what is inside it or, read
// as "anything in here could match", every pattern at once; the rule arrives
// with the call that reads or writes a matching file instead. A path that does
// not exist yet - a file about to be written - is kept.
func withoutDirectories(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deliveredRulePaths returns the attachment paths of the rules the model can
// read in msgs: the rule attachments of user messages (a rule the user named,
// one a mentioned path activated) and of tool results (one a call activated),
// in the window the provider is sent - everything from the last compaction
// summary on. The summary itself does not count: whatever of a rule a
// summarizer retold is not the rule, and the next match attaches it whole.
func deliveredRulePaths(msgs []llm.Message) map[string]bool {
	out := map[string]bool{}
	for _, m := range session.MessagesForLLM(msgs) {
		if m.CompactionSummary {
			continue
		}
		var text string
		switch m.Role {
		case llm.RoleUser:
			text = m.Content
		case llm.RoleTool:
			text = m.Rules
		}
		if !strings.Contains(text, `kind="rule"`) {
			continue
		}
		for _, blk := range mention.Blocks(text) {
			if blk.Kind == mention.KindRule {
				out[blk.Path] = true
			}
		}
	}
	return out
}

// freshRules returns the candidates whose attachment path is not in delivered,
// each once. delivered is updated with what it returns.
func freshRules(delivered map[string]bool, cwd, home string, candidates []*rules.Rule) []*rules.Rule {
	var out []*rules.Rule
	for _, r := range candidates {
		if r == nil {
			continue
		}
		p := session.RuleAttachmentPath(cwd, home, r)
		if delivered[p] {
			continue
		}
		delivered[p] = true
		out = append(out, r)
	}
	return out
}

// withToolRules returns msgs as the provider is sent them: the rules a tool
// call brought in joined to the end of its result. The input is never written,
// so the working slice and the transcript keep the output and the rules apart.
func withToolRules(msgs []llm.Message) []llm.Message {
	var out []llm.Message
	for i, m := range msgs {
		if m.Rules == "" {
			continue
		}
		if out == nil {
			out = append([]llm.Message(nil), msgs...)
		}
		out[i].Content = joinToolRules(m.Content, m.Rules)
		out[i].Rules = ""
	}
	if out == nil {
		return msgs
	}
	return out
}

// joinToolRules is a tool result's content followed by the rules it carries.
func joinToolRules(content, rulesText string) string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return rulesText
	}
	return content + "\n\n" + rulesText
}

// rulesRendered reports whether the template this mode runs on prints
// {{.Rules}}. A template under prompts.dir without it asked for no rules at
// all, so none is attached to a tool result behind its back either; nor to the
// results of a system child that carries a template of its own.
func (a *Agent) rulesRendered(mode string) bool {
	if a.subagent != nil && strings.TrimSpace(a.subagent.PromptTemplate) != "" {
		return false
	}
	promptsDir := a.cfg.Prompts.ResolvedDir(a.state.GetCWD())
	return prompts.RendersRules(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), a.cfg.Prompts.AskFile())
}

// rereadRules discovers the session's rules afresh, which starts a new rules
// generation: the next system prompt reads the always-on rules, the AGENTS.md
// pair and the instruction files from disk again.
func (a *Agent) rereadRules() {
	st := sessionStatePtr(a.state)
	if st == nil {
		return
	}
	st.ReplaceRulesCatalog(session.DiscoverRules(a.cfg, st.GetCWD()))
}

// agentsOnDemand reports whether nested AGENTS.md files are read for the
// folders a tool enters: rules discovery is on and rules.systems does not
// exclude the agents system.
func (a *Agent) agentsOnDemand() bool {
	if a == nil || a.cfg == nil || !a.cfg.Rules.AutoDiscoverEnabled() {
		return false
	}
	return rules.AgentsOnDemand(rules.ParseSystems(a.cfg.Rules.Systems))
}
