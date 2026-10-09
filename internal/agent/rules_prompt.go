package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// rulesState is implemented by session.State for rules prompt wiring.
type rulesState interface {
	GetCWD() string
	GetRulesCatalog() []*rules.Rule
	GetMessages() []llm.Message
	GetLastContextBreakdown() *session.ContextBreakdown
	SetLastContextBreakdown(*session.ContextBreakdown)
	CachedRulesPrompt(inputs string) (*session.RulesPrompt, uint64)
	StoreRulesPrompt(*session.RulesPrompt)
}

// standingParts returns the part of the system prompt that does not move
// while a rules generation lasts (issue #425): the AGENTS.md and DESIGN.md of
// the agent home and of the session folder, always and in that order (Docs),
// the rules that always apply (Rules), the files of instructions.files after
// all of them (User), and the key of every file Docs and User carry (Keys),
// each file once. It is rendered once per generation and reused by every
// later turn, whatever template a turn runs on, so neither a rule that
// activates nor an AGENTS.md edited mid-session moves the system message the
// provider has cached; a compaction, a config reload, a workspace switch or a
// restart starts the next generation, which reads the files again. A rule
// scoped to paths and a nested AGENTS.md are never part of it: they arrive
// with the tool result or the message that brought their path into play
// (rules_activation.go, mentions.go), unless Keys already holds their file.
func (a *Agent) standingParts() *session.RulesPrompt {
	cwd, home := a.state.GetCWD(), a.cfg.Paths.Home
	render := func() *session.RulesPrompt {
		st := rules.LoadStanding(home, cwd, session.ResolveInstructionFiles(a.cfg.Instructions.Files, cwd, home))
		// LoadStanding skips a file it cannot read without a word, which is
		// right for a workspace file this workspace does not carry and wrong
		// for one the operator named for every session: say so once per
		// generation, where the operator of the process looks.
		for _, u := range session.UnreadInstructionFiles(a.cfg.Instructions.Files, cwd, home) {
			a.log.Warn("instructions file not read",
				"entry", fmt.Sprintf("instructions.files[%d]", u.Index),
				"path", u.Path,
				"reason", u.Reason())
		}
		return &session.RulesPrompt{
			Docs: rules.RenderDocs(st.Docs),
			User: rules.RenderUserDocs(st.User),
			Keys: st.Keys,
		}
	}
	rs, ok := a.state.(rulesState)
	if !ok {
		return render()
	}
	// A configuration reloaded without a new generation - another agent home,
	// another instructions.files list - renders afresh; the files behind an
	// unchanged configuration are read once per generation.
	inputs := strings.Join(append([]string{home, cwd}, a.cfg.Instructions.Files...), "\x00")
	cached, generation := rs.CachedRulesPrompt(inputs)
	if cached != nil {
		return cached
	}
	p := render()
	p.Generation, p.Inputs = generation, inputs
	p.Rules = rules.RenderSection("## Active project rules", rules.AlwaysOnRules(rs.GetRulesCatalog()))
	rs.StoreRulesPrompt(p)
	return p
}

// documentKeys are the keys of the documents the system prompt the model is
// reading carries: those of the build this turn froze, else those of the
// current generation, which the next build will carry. A tool call reads
// them, so a reload from another surface in the middle of the turn cannot
// make a nested document the frozen prompt already holds look new.
func (a *Agent) documentKeys() map[string]bool {
	a.docKeysMu.Lock()
	keys := a.docKeys
	a.docKeysMu.Unlock()
	if keys != nil {
		return keys
	}
	return a.standingParts().Keys
}

func (a *Agent) setDocumentKeys(keys map[string]bool) {
	if keys == nil {
		keys = map[string]bool{}
	}
	a.docKeysMu.Lock()
	a.docKeys = keys
	a.docKeysMu.Unlock()
}

// computeContextBreakdown estimates category sizes for the context UI.
// fullSystem is the rendered system message; tools/skills/rules are subtracted for SystemPrompt.
// readsImages says whether the pictures of the messages go out with them.
func computeContextBreakdown(
	fullSystem string,
	skillsMD, toolsMD, rulesMD string,
	messages []llm.Message,
	readsImages bool,
	toolDefs []llm.ToolDefinition,
) *session.ContextBreakdown {
	toolsMDTok := session.EstimateContextTokens(toolsMD)
	toolsTok := toolsMDTok
	rulesTok := session.EstimateContextTokens(rulesMD)
	skillsTok := session.EstimateContextTokens(skillsMD)
	var mcpTok int
	// toolsMD already includes names and descriptions. The provider receives
	// schemas separately, so add those without counting the names twice.
	for _, def := range toolDefs {
		encoded, err := json.Marshal(def.InputSchema)
		if err != nil {
			continue
		}
		if strings.Contains(def.Name, "__") {
			mcpTok += session.EstimateContextTokens(string(encoded))
		} else {
			toolsTok += session.EstimateContextTokens(string(encoded))
		}
	}
	convTok := conversationTokens(messages, readsImages)
	fullTok := session.EstimateContextTokens(fullSystem)
	sysTok := fullTok - toolsMDTok - rulesTok - skillsTok
	if sysTok < 0 {
		sysTok = 0
	}
	b := &session.ContextBreakdown{
		SystemPrompt:    sysTok,
		ToolDefinitions: toolsTok,
		Rules:           rulesTok,
		Skills:          skillsTok,
		MCP:             mcpTok,
		Subagents:       0,
		Conversation:    convTok,
	}
	b.Sum()
	return b
}

// conversationText is the conversation as the provider reads it, the rules a
// tool call brought in joined to its result, for the token estimates.
func conversationText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" && m.Rules == "" {
			continue
		}
		b.WriteString(string(m.Role))
		b.WriteString(":\n")
		if m.Rules != "" {
			b.WriteString(joinToolRules(m.Content, m.Rules))
		} else {
			b.WriteString(m.Content)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// FilterSkillsForContext wraps skills filter (unchanged semantics for skills only).
func FilterSkillsForContext(all []*skills.Skill, contextFiles []string) []*skills.Skill {
	return skills.FilterForContext(all, contextFiles)
}
