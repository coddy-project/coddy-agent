package agent

// The agent's half of mentions. The manager resolves every "@" reference of
// a prompt into attachment resources (internal/session/mentions.go); here
// they are written into the user message, and the rules the mentioned paths
// activate - a glob rule, a nested AGENTS.md - ride in that same message as
// attachments of their own. A rule the model can read in its history is not
// repeated in the system prompt, so a mention never moves the system message
// and never costs the provider's cached copy of the conversation behind it.

import (
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// resourceAttachmentXML renders one resolved resource as the
// <coddy_attachment> element the model reads (mention.Attachment). A ranged
// resource carries its lines as the "#L<start>-<end>" fragment that
// internal/session wrote; the same parser takes it back off the path.
func resourceAttachmentXML(res *acp.Resource) string {
	uri := strings.TrimSpace(res.URI)
	if strings.HasPrefix(uri, "file://") {
		uri = fileURIPath(uri)
	}
	base, startLine, endLine := session.SplitLineRangeURI(uri)
	att := mention.Attachment{Path: filepath.ToSlash(base), Body: res.Text}
	if startLine > 0 {
		att.Lines = mention.Range{Start: startLine, End: endLine}
	}
	if m := res.Mention; m != nil {
		att.Kind, att.Name, att.Typed = m.Kind, m.Name, m.Typed
	}
	return att.XML()
}

// attachActivatedRules appends to blocks the path-scoped rules the prompt's
// mentioned files and folders activate for the first time: a rule whose globs
// match one of them, and the nested AGENTS.md (and DESIGN.md) files on the
// chain of folders down to each. They join the sticky set, as a tool call's
// activation does, but their text travels in the user message instead of the
// system prompt: the system message this turn starts with stays the one the
// previous turn ended with.
func (a *Agent) attachActivatedRules(blocks []acp.ContentBlock) []acp.ContentBlock {
	rs, ok := a.state.(rulesState)
	if !ok {
		return blocks
	}
	paths := extractContextFiles(blocks)
	if len(paths) == 0 {
		return blocks
	}
	cwd := rs.GetCWD()
	active := rs.GetActiveAutoRules()
	var newly []*rules.Rule
	for _, r := range rules.MatchAuto(rs.GetRulesCatalog(), paths) {
		// Only a rule a path gates: an always-on rule belongs to the system
		// prompt from the first turn on.
		if r != nil && (r.ScopeDir != "" || len(r.Globs) > 0) {
			newly = append(newly, r)
		}
	}
	if a.agentsOnDemand() {
		newly = append(newly, rules.AgentsForPaths(cwd, paths, active)...)
	}
	newly = rules.Added(active, newly)
	if len(newly) == 0 {
		return blocks
	}
	rs.SetActiveAutoRules(rules.UnionStable(active, newly))
	home := a.homeDir()
	for _, r := range rules.UnionStable(nil, newly) {
		blocks = append(blocks, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: session.RuleAttachment(cwd, home, r)})
	}
	return blocks
}

// homeDir is the operator's home as mentions resolve "~" against it.
func (a *Agent) homeDir() string {
	return mention.HomeDir()
}

// rulesInHistory returns the IDs of the catalog rules whose attachment the
// model can still read in msgs: an explicitly mentioned rule, or one a
// mentioned path activated. A compaction folds those messages into its
// summary, and the rule then comes back through the system prompt.
func rulesInHistory(msgs []llm.Message, cwd, home string, catalog, active []*rules.Rule) map[string]bool {
	paths := map[string]bool{}
	for _, m := range session.MessagesForLLM(msgs) {
		if m.Role != llm.RoleUser || !strings.Contains(m.Content, `kind="rule"`) {
			continue
		}
		for _, blk := range mention.Blocks(m.Content) {
			if blk.Kind == mention.KindRule {
				paths[blk.Path] = true
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, list := range [][]*rules.Rule{catalog, active} {
		for _, r := range list {
			if r != nil && paths[session.RuleAttachmentPath(cwd, home, r)] {
				out[r.ID] = true
			}
		}
	}
	return out
}

// withoutRules drops the rules whose ID is in skip.
func withoutRules(rs []*rules.Rule, skip map[string]bool) []*rules.Rule {
	if len(skip) == 0 {
		return rs
	}
	out := make([]*rules.Rule, 0, len(rs))
	for _, r := range rs {
		if r != nil && !skip[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// resolveQueuedMessage turns a follow-up read from the queue into the content
// of its user message: its "@" references resolved the way the manager
// resolves a prompt's, and the rules its mentioned paths activate attached.
func (a *Agent) resolveQueuedMessage(text string) string {
	blocks := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}
	if st := sessionStatePtr(a.state); st != nil {
		blocks = st.ResolveQueuedMentions(blocks)
	}
	blocks = append(blocks, invokedSkillBlocks(text, a.state.GetSkills())...)
	blocks = a.attachActivatedRules(blocks)
	return contentBlocksToText(blocks)
}
