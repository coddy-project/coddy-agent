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

// buildRulesPromptMarkdown renders the {{.Rules}} block for one request and
// reports the project docs it embedded, which the instructions block then
// leaves alone. With agentsOnDemand the nested AGENTS.md files on the chain
// down to every attached file:// path are read here, the same way a filesystem
// tool call reads them (activateScopedRulesForToolCall); both stick for the
// session.
func buildRulesPromptMarkdown(st rulesState, home string, contextFiles []string, userText string, agentsOnDemand bool) (string, []string) {
	catalog := st.GetRulesCatalog()
	active := st.GetActiveAutoRules()
	newAuto := rules.MatchAuto(catalog, contextFiles)
	if agentsOnDemand {
		newAuto = append(newAuto, rules.AgentsForPaths(st.GetCWD(), contextFiles, active)...)
	}
	sticky := rules.UnionStable(active, newAuto)
	st.SetActiveAutoRules(sticky)
	mentioned := rules.SelectMentioned(catalog, userText)
	return rules.RenderPrompt(home, st.GetCWD(), sticky, mentioned)
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
