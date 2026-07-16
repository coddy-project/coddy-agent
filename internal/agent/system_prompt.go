package agent

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

func joinNonEmptyPromptBlocks(parts ...string) string {
	var b []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			b = append(b, p)
		}
	}
	return strings.Join(b, "\n\n")
}

// buildSkillsPromptMarkdown merges the slash catalog and bodies of context-matched non-command skills.
// Slash-command bodies (invoked via /name) are NOT included here — they are injected directly into
// the user message by buildMessages so the LLM sees them close to the user's request.
func buildSkillsPromptMarkdown(allLoaded []*skills.Skill, active []*skills.Skill) string {
	activeDedup := skills.DedupeSkillsByCanonicalName(active)
	skillSums := skills.ListSkills(allLoaded)
	catalogNameSet := make(map[string]struct{}, len(skillSums))
	for _, s := range skillSums {
		catalogNameSet[s.Name] = struct{}{}
	}

	// Active section: skill bodies for context-matched skills that are NOT slash commands.
	// Slash commands are listed in the catalog only; their bodies are injected into the user
	// message on explicit invocation so the LLM sees them as close to the request as possible.
	var activeForSection []*skills.Skill
	for _, sk := range activeDedup {
		n := skills.CanonicalCommandName(sk)
		if n != "" {
			if _, inCat := catalogNameSet[n]; inCat {
				continue
			}
		}
		activeForSection = append(activeForSection, sk)
	}

	catalog := skills.BuildSlashCatalogMarkdown(skillSums)
	section := skills.BuildSystemPromptSection(activeForSection)
	return joinNonEmptyPromptBlocks(catalog, section)
}

// buildSystemPrompt constructs the system prompt for the current mode and skills.
// It is rebuilt each agent turn so the checklist section stays aligned with todo tool mutations.
func (a *Agent) buildSystemPrompt(mode string, activeSkills []*skills.Skill, toolDefs []llm.ToolDefinition, userText string, contextFiles []string) string {
	promptsDir := a.cfg.Prompts.ResolvedDir(a.state.GetCWD())
	promptTodoMD := checklistMarkdownFromPlan(a.state.GetPlan())
	mem := formatMergedMemory(strings.TrimSpace(a.state.GetAgentMemory()), strings.TrimSpace(a.state.GetMemoryCopilotBlock()))
	planCtx := ""
	if mode == "agent" {
		planCtx = a.state.TakePendingPlanContext()
	}
	discardedPlans := ""
	if mode == "plan" {
		discardedPlans = discardedPlansPromptBlock(a.state.DiscardedPlanSlugs())
	}
	skillsMD := buildSkillsPromptMarkdown(a.state.GetSkills(), activeSkills)
	toolsMD := tools.FormatDefinitionsForPrompt(toolDefs)
	rulesMD := ""
	if rs, ok := a.state.(rulesState); ok {
		rulesMD = buildRulesPromptMarkdown(rs, contextFiles, userText)
	}
	instructionsMD := session.LoadInstructions(a.state.GetCWD(), a.cfg.Instructions.Files)
	full := prompts.RenderWithFallback(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), prompts.TemplateData{
		CWD:            a.state.GetCWD(),
		Skills:         skillsMD,
		Rules:          rulesMD,
		Tools:          toolsMD,
		Memory:         mem,
		TodoList:       promptTodoMD,
		PlanContext:    planCtx,
		DiscardedPlans: discardedPlans,
		Instructions:   instructionsMD,
		UTCNow:         time.Now().UTC().Format(time.RFC3339),
	})
	full = joinNonEmptyPromptBlocks(full, a.environment.PromptContext())
	if rs, ok := a.state.(rulesState); ok {
		rs.SetLastContextBreakdown(computeContextBreakdown(full, skillsMD, toolsMD, rulesMD, a.state.GetMessages(), toolDefs))
	}
	return full
}

func discardedPlansPromptBlock(slugs []string) string {
	if len(slugs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Discarded design plans\n\n")
	b.WriteString("The user discarded these plan files in the UI. Do **not** reuse their slugs or recycle their plan titles. ")
	b.WriteString("When you write a new design plan, pick a **fresh slug** and a **new name** until the user leaves plan mode.\n\n")
	for _, slug := range slugs {
		b.WriteString("- `")
		b.WriteString(slug)
		b.WriteString("`\n")
	}
	return strings.TrimSpace(b.String())
}

// checklistMarkdownFromPlan renders the session plan for embedding in prompts (trimmed checklist text).
func checklistMarkdownFromPlan(entries []acp.PlanEntry) string {
	return strings.TrimSpace(todo.FormatPlanMarkdown(entries))
}

func formatMergedMemory(sessionNotes, recall string) string {
	var parts []string
	if recall != "" {
		parts = append(parts, recall)
	}
	if sessionNotes != "" {
		parts = append(parts, "Session notes:\n"+sessionNotes)
	}
	return strings.Join(parts, "\n\n")
}
