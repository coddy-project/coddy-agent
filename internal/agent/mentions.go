package agent

// The agent's half of mentions. The manager resolves every "@" reference of
// a prompt into attachment resources (internal/session/mentions.go); here
// they are written into the user message, and the rules the mentioned paths
// activate - a glob rule, a nested AGENTS.md - ride in that same message as
// attachments of their own. The system prompt carries only the rules that
// always apply, so a mention never moves the system message and never costs
// the provider's cached copy of the conversation behind it.

import (
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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
// mentioned files and folders bring into play: a rule whose globs match one of
// them, and the nested AGENTS.md (and DESIGN.md) files on the chain of folders
// down to each. Their text travels in the user message, never in the system
// prompt, so the system message this turn starts with stays the one the
// previous turn ended with. A rule the model can already read with the same
// text - attached to a message or a tool result it is still sent, or named in
// this very prompt - is not attached again, and neither is a rule document the
// prompt attaches as a file of its own, nor a document the system prompt
// already carries. A template without {{.Rules}} gets no glob rule, as it gets
// none with a tool result; the nested documents come whatever it prints.
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
	var matched []*rules.Rule
	if a.rulesRendered(a.state.EffectiveMode()) {
		for _, r := range rules.MatchAuto(rs.GetRulesCatalog(), paths) {
			// Only a rule a path gates: an always-on rule is in the system
			// prompt from the first turn on.
			if r != nil && !r.AlwaysOn() {
				matched = append(matched, r)
			}
		}
	}
	matched = append(matched, a.nestedDocuments(cwd, paths)...)
	matched = withoutTargets(matched, paths)
	// The first turn of a generation has no system prompt built yet: the
	// keys come from the standing parts it will carry (documentKeys).
	matched = withoutPromptDocuments(matched, a.documentKeys())
	delivered := deliveredRules(rs.GetMessages())
	for _, b := range blocks {
		if res := b.Resource; res != nil && res.Mention != nil && res.Mention.Kind == mention.KindRule {
			path, body := ruleAttachmentKey(res)
			delivered[path] = body
		}
	}
	home := a.homeDir()
	for _, r := range freshRules(delivered, cwd, home, matched) {
		blocks = append(blocks, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: session.RuleAttachment(cwd, home, r)})
	}
	return blocks
}

// homeDir is the operator's home as mentions resolve "~" against it.
func (a *Agent) homeDir() string {
	return mention.HomeDir()
}

// resolveQueuedMessage turns a follow-up read from the queue into the content
// of its user message: its "@" references resolved the way the manager
// resolves a prompt's, and the rules its mentioned paths activate attached.
func (a *Agent) resolveQueuedMessage(text string) string {
	blocks := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}
	if st := sessionStatePtr(a.state); st != nil {
		blocks = st.ResolveQueuedMentions(blocks)
	}
	blocks = append(blocks, invokedSkillBlocks(text, a.state.GetSkills(), a.managedSkillsDir())...)
	blocks = a.attachActivatedRules(blocks)
	return contentBlocksToText(blocks)
}
