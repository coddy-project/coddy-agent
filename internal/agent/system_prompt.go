package agent

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
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

// buildSkillsPromptMarkdown merges slash catalog, active globs-linked skill bodies, and ephemeral /name invokes.
func buildSkillsPromptMarkdown(allLoaded []*skills.Skill, active []*skills.Skill, userText string) string {
	activeDedup := skills.DedupeSkillsByCanonicalName(active)
	activeGlobCanon := make(map[string]struct{})
	for _, sk := range activeDedup {
		n := skills.CanonicalCommandName(sk)
		if n != "" {
			activeGlobCanon[n] = struct{}{}
		}
	}
	var filteredInvoke []string
	for _, n := range skills.ParseInvokedCommandNames(userText) {
		if _, ok := activeGlobCanon[n]; ok {
			continue
		}
		filteredInvoke = append(filteredInvoke, n)
	}
	skillSums := skills.ListSkills(allLoaded)
	catalogNameSet := make(map[string]struct{}, len(skillSums))
	for _, s := range skillSums {
		catalogNameSet[s.Name] = struct{}{}
	}
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
	ephemeral := skills.BuildInvokedSkillsSection(allLoaded, filteredInvoke)
	return joinNonEmptyPromptBlocks(catalog, section, ephemeral)
}

// buildSystemPrompt constructs the system prompt for the current mode and skills.
// It is rebuilt each agent turn so the checklist section stays aligned with todo tool mutations.
func (a *Agent) buildSystemPrompt(mode string, activeSkills []*skills.Skill, toolDefs []llm.ToolDefinition, userText string) string {
	promptsDir := a.cfg.Prompts.ResolvedDir(a.state.GetCWD())
	promptTodoMD := checklistMarkdownFromPlan(a.state.GetPlan())
	mem := formatMergedMemory(strings.TrimSpace(a.state.GetAgentMemory()), strings.TrimSpace(a.state.GetMemoryCopilotBlock()))
	return prompts.RenderWithFallback(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), prompts.TemplateData{
		CWD:      a.state.GetCWD(),
		Skills:   buildSkillsPromptMarkdown(a.state.GetSkills(), activeSkills, userText),
		Tools:    tools.FormatDefinitionsForPrompt(toolDefs),
		Memory:   mem,
		TodoList: promptTodoMD,
		UTCNow:   time.Now().UTC().Format(time.RFC3339),
	})
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
