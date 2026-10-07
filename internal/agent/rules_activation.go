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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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
// resumes the call). A rule document the call itself targets is left out: a
// read carries its text in the output, a write carries the text the model is
// putting there. So is a rule the model can already read with the same text,
// attached to an earlier result or message it is still sent; one a compaction
// folded away, or whose file has changed since it was attached, comes with the
// next call that matches it. It is the empty string when there is nothing new.
func (a *Agent) toolCallRules(mode string, tc llm.ToolCall, cwd string) string {
	rs, ok := a.state.(rulesState)
	if !ok {
		return ""
	}
	paths := toolfs.ToolCallPaths(tc.Name, tc.InputJSON, cwd)
	if len(paths) == 0 {
		return ""
	}
	var matched []*rules.Rule
	if a.rulesRendered(mode) {
		matched = rules.MatchScoped(rs.GetRulesCatalog(), withoutDirectories(paths))
	}
	matched = append(matched, a.nestedDocuments(cwd, paths)...)
	matched = withoutTargets(matched, paths)
	matched = withoutPromptDocuments(matched, a.documentKeys())
	if len(matched) == 0 {
		return ""
	}
	home := a.homeDir()
	fresh := freshRules(deliveredRules(rs.GetMessages()), cwd, home, matched)
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

// nestedDocuments returns the nested AGENTS.md and DESIGN.md files on the
// chain of folders down to paths, read on the spot: the third layer of the
// documents a session always reads (issue #425). No rules setting and no
// prompt template turns the chain off. A system child that runs on a template
// of its own (the memory subagent) gets none: its task is not the
// workspace's.
func (a *Agent) nestedDocuments(cwd string, paths []string) []*rules.Rule {
	if a.subagent != nil && strings.TrimSpace(a.subagent.PromptTemplate) != "" {
		return nil
	}
	return rules.AgentsForPaths(cwd, paths, nil)
}

// withoutPromptDocuments drops the candidates whose file the system prompt
// already carries (keys, by rules.DocKey): a file enters the prompt once per
// rules generation, so a nested AGENTS.md that instructions.files names, or a
// folder that is the agent home, is not attached a second time.
func withoutPromptDocuments(rs []*rules.Rule, keys map[string]bool) []*rules.Rule {
	if len(keys) == 0 {
		return rs
	}
	out := rs[:0:0]
	for _, r := range rs {
		if r != nil && !keys[rules.DocKey(r.FilePath)] {
			out = append(out, r)
		}
	}
	return out
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

// withoutTargets drops the rules whose own file is one of paths.
func withoutTargets(rs []*rules.Rule, paths []string) []*rules.Rule {
	out := rs[:0:0]
	for _, r := range rs {
		if r == nil {
			continue
		}
		targeted := false
		for _, p := range paths {
			if rules.SamePath(r.FilePath, p) {
				targeted = true
				break
			}
		}
		if !targeted {
			out = append(out, r)
		}
	}
	return out
}

// deliveredRules returns the rules the model can read in msgs, by the path of
// their attachment, with the text each was last attached with: the rule
// attachments of user messages (a rule the user named, one a mentioned path
// activated) and of tool results (one a call activated), in the window the
// provider is sent - everything from the last compaction summary on. The
// summary itself does not count: whatever of a rule a summarizer retold is not
// the rule, and the next match attaches it whole.
func deliveredRules(msgs []llm.Message) map[string]string {
	out := map[string]string{}
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
				out[blk.Path] = blk.Body(text)
			}
		}
	}
	return out
}

// ruleAttachmentKey is how an attached rule resource is known in the history:
// the path and the text its <coddy_attachment> element carries, read back from
// the element itself so the two sides can never spell the path differently.
func ruleAttachmentKey(res *acp.Resource) (path, body string) {
	element := resourceAttachmentXML(res)
	for _, blk := range mention.Blocks(element) {
		return blk.Path, blk.Body(element)
	}
	return res.URI, res.Text
}

// freshRules returns the candidates the model cannot read yet, each once: a
// rule never attached in delivered, or attached with other text - its file
// changed and was read again since. A file is known by its real path
// (rules.DocKey) as well as by the path its attachment names, so the same
// document reached through a link is not attached twice. delivered is updated
// with what it returns.
func freshRules(delivered map[string]string, cwd, home string, candidates []*rules.Rule) []*rules.Rule {
	byKey := make(map[string]string, len(delivered))
	for path, body := range delivered {
		if loc, ok := mention.Resolve(cwd, home, path); ok {
			byKey[rules.DocKey(loc.Abs)] = body
		}
	}
	var out []*rules.Rule
	for _, r := range candidates {
		if r == nil {
			continue
		}
		path, body := ruleAttachmentKey(session.RuleAttachment(cwd, home, r))
		if sent, ok := delivered[path]; ok && sent == body {
			continue
		}
		key := rules.DocKey(r.FilePath)
		if sent, ok := byKey[key]; ok && key != "" && sent == body {
			continue
		}
		delivered[path] = body
		if key != "" {
			byKey[key] = body
		}
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
// all, so no glob rule is attached to a tool result behind its back either;
// nor to the results of a system child that carries a template of its own.
// The AGENTS.md documents are not rules in this sense: they reach the prompt
// whatever the template prints (renderWithDocuments, nestedDocuments).
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
