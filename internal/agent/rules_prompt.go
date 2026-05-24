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
	GetActiveAutoRules() []*rules.Rule
	SetActiveAutoRules([]*rules.Rule)
	GetMessages() []llm.Message
	GetLastContextBreakdown() *session.ContextBreakdown
	SetLastContextBreakdown(*session.ContextBreakdown)
}

func buildRulesPromptMarkdown(st rulesState, contextFiles []string, userText string) string {
	catalog := st.GetRulesCatalog()
	newAuto := rules.MatchAuto(catalog, contextFiles)
	sticky := rules.UnionStable(st.GetActiveAutoRules(), newAuto)
	st.SetActiveAutoRules(sticky)
	mentioned := rules.SelectMentioned(catalog, userText)
	return rules.RenderPrompt(st.GetCWD(), sticky, mentioned)
}

// computeContextBreakdown estimates category sizes for the context UI.
func computeContextBreakdown(
	skillsMD, toolsMD, rulesMD string,
	messages []llm.Message,
	toolDefs []llm.ToolDefinition,
) *session.ContextBreakdown {
	conv := conversationText(messages)
	b := &session.ContextBreakdown{
		SystemPrompt:    0,
		ToolDefinitions: session.EstimateTokens(toolsMD),
		Rules:           session.EstimateTokens(rulesMD),
		Skills:          session.EstimateTokens(skillsMD),
		MCP:             estimateMCPTokens(toolDefs),
		Subagents:       0,
		Conversation:    session.EstimateTokens(conv),
	}
	b.Sum()
	return b
}

func conversationText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		b.WriteString(string(m.Role))
		b.WriteString(":\n")
		b.WriteString(m.Content)
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
