package agent

import (
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
	CachedRulesPrompt(rendersRules bool) (*session.RulesPrompt, uint64)
	StoreRulesPrompt(*session.RulesPrompt)
}

// standingPrompt returns the {{.Rules}} and {{.Instructions}} blocks of the
// system prompt: the project docs preamble and the always-on rules, then the
// files of instructions.files. They are rendered once per rules generation of
// the session and reused by every later turn (session.RulesPrompt), so neither a
// rule that activates nor an AGENTS.md edited mid-session moves the system
// message the provider has cached; a compaction, a config reload, a workspace
// switch or a restart starts the next generation, which reads the files again.
// A rule scoped to paths is never part of it: it arrives with the tool result
// or the message that brought its path into play (rules_activation.go,
// mentions.go).
func (a *Agent) standingPrompt(rendersRules bool) (rulesMD, instructionsMD string) {
	cwd, home := a.state.GetCWD(), a.cfg.Paths.Home
	rs, ok := a.state.(rulesState)
	if !ok {
		return "", session.LoadInstructions(cwd, home, a.cfg.Instructions.Files, nil)
	}
	cached, generation := rs.CachedRulesPrompt(rendersRules)
	if cached != nil {
		return cached.Rules, cached.Instructions
	}
	rulesMD, embedded := rules.RenderPrompt(home, cwd, rules.AlwaysOnRules(rs.GetRulesCatalog()))
	// Project docs the rules block already carries: instructions.files names
	// AGENTS.md too, and one system prompt does not need it twice. A template
	// under prompts.dir may render {{.Instructions}} and not {{.Rules}}, and
	// then nothing carries them - so the skip list is taken only from a
	// template that actually prints the block.
	if !rendersRules {
		embedded = nil
	}
	instructionsMD = session.LoadInstructions(cwd, home, a.cfg.Instructions.Files, embedded)
	rs.StoreRulesPrompt(&session.RulesPrompt{
		Generation:   generation,
		RendersRules: rendersRules,
		Rules:        rulesMD,
		Instructions: instructionsMD,
	})
	return rulesMD, instructionsMD
}

// computeContextBreakdown estimates category sizes for the context UI.
// fullSystem is the rendered system message; tools/skills/rules are subtracted for SystemPrompt.
func computeContextBreakdown(
	fullSystem string,
	skillsMD, toolsMD, rulesMD string,
	messages []llm.Message,
	toolDefs []llm.ToolDefinition,
) *session.ContextBreakdown {
	toolsTok := session.EstimateTokens(toolsMD)
	rulesTok := session.EstimateTokens(rulesMD)
	skillsTok := session.EstimateTokens(skillsMD)
	mcpTok := estimateMCPTokens(toolDefs)
	convTok := session.EstimateTokens(conversationText(messages))
	fullTok := session.EstimateTokens(fullSystem)
	sysTok := fullTok - toolsTok - rulesTok - skillsTok
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
		b.WriteString(m.Content)
		if m.Rules != "" {
			b.WriteString("\n\n")
			b.WriteString(m.Rules)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

func estimateMCPTokens(defs []llm.ToolDefinition) int {
	var b strings.Builder
	for _, d := range defs {
		if strings.Contains(d.Name, "__") {
			b.WriteString(d.Name)
			b.WriteString(d.Description)
		}
	}
	if b.Len() == 0 {
		return 0
	}
	return session.EstimateTokens(b.String())
}

// FilterSkillsForContext wraps skills filter (unchanged semantics for skills only).
func FilterSkillsForContext(all []*skills.Skill, contextFiles []string) []*skills.Skill {
	return skills.FilterForContext(all, contextFiles)
}
