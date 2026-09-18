package agent

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
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

// loadSkillHint tells the model it may pull a catalogued skill's full instructions
// into the turn on its own via the load_skill tool (model-driven auto-discovery).
const loadSkillHint = "When the user's request matches one of the slash commands above, call the `load_skill` tool with that command's name to load its full instructions before acting."

// buildSkillsPromptMarkdown merges the slash catalog and bodies of context-matched non-command skills.
// Slash-command bodies (invoked via /name) are NOT included here — they are injected directly into
// the user message by buildMessages so the LLM sees them close to the user's request.
// When autoDiscovery is set, the catalog is followed by loadSkillHint so the model knows it can
// fetch a matching skill's body itself instead of waiting for an explicit /name.
func buildSkillsPromptMarkdown(allLoaded []*skills.Skill, active []*skills.Skill, autoDiscovery bool) string {
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
	if autoDiscovery && len(skillSums) > 0 {
		catalog = joinNonEmptyPromptBlocks(catalog, loadSkillHint)
	}
	section := skills.BuildSystemPromptSection(activeForSection)
	return joinNonEmptyPromptBlocks(catalog, section)
}

// loadSkillBody backs the model-driven load_skill tool: it returns a loaded
// skill's body by canonical command name plus every available command name for
// the current session cwd.
func (a *Agent) loadSkillBody(name string) (string, []string, bool) {
	idx := skills.SkillBySlashName(a.state.GetSkills())
	available := make([]string, 0, len(idx))
	for n := range idx {
		available = append(available, n)
	}
	if sk, ok := idx[strings.TrimSpace(name)]; ok {
		return strings.TrimSpace(sk.Content), available, true
	}
	return "", available, false
}

// systemPromptBuild is a rendered system message plus what the turn still needs
// from it once it is frozen: the component blocks the context estimate
// subtracts, the tool definitions it described, and the sticky rule set it
// already carries, so a rule activated later can be told apart from one the
// model has already been given.
type systemPromptBuild struct {
	Mode     string
	Content  string
	SkillsMD string
	ToolsMD  string
	RulesMD  string
	ToolDefs []llm.ToolDefinition
	// RenderedRules is what the {{.Rules}} block of this message carries, and
	// what the turn context block diffs a later activation against.
	RenderedRules []*rules.Rule
	// Clock is the wall clock this build was stamped with: the {{.UTCNow}} a
	// template may render and the reading the turn context block carries. It is
	// taken once per turn and reused by every step, so a step the lane re-issues
	// sends byte for byte the request that failed (react.go, lane replays)
	// rather than one that ticked a second forward.
	Clock time.Time
	// RendersRules is false for a template under prompts.dir with no
	// {{.Rules}} in it. That operator asked for no rules block at all, so a
	// rule a tool call activates is not smuggled in after the history either.
	RendersRules bool
	// Volatile marks a template under prompts.dir that prints {{.UTCNow}} or
	// {{.TodoList}}. Such a message cannot be frozen for the turn - its own
	// conditionals have to keep matching the state - so the loop re-renders it
	// before every call, as it did for every template before, and sends no turn
	// context block: the template is already carrying what the block would say.
	Volatile bool
}

// buildSystemPrompt constructs the system prompt for the current mode and skills.
func (a *Agent) buildSystemPrompt(mode string, activeSkills []*skills.Skill, toolDefs []llm.ToolDefinition, userText string, contextFiles []string) string {
	return a.buildSystemPromptParts(mode, activeSkills, toolDefs, userText, contextFiles).Content
}

// buildSystemPromptParts renders the system message for the turn. It is built
// once per turn and then frozen: a provider caches a request by its prefix, and
// the system message sits in front of the whole conversation, so rewriting it
// between the steps of a turn throws away the cached copy of everything behind
// it. What moves while the turn runs - the wall clock, the todo checklist, the
// rules a tool call activated - travels in the turn context block appended
// after the history instead (turn_context.go). So does the memory subagent's
// report, which moves between turns: a recall answers one message, and a
// report rendered here would make every turn's system message a new one and
// cost the cached copy of the whole conversation each time.
func (a *Agent) buildSystemPromptParts(mode string, activeSkills []*skills.Skill, toolDefs []llm.ToolDefinition, userText string, contextFiles []string) *systemPromptBuild {
	promptsDir := a.cfg.Prompts.ResolvedDir(a.state.GetCWD())
	clock := a.now().UTC()
	if a.subagent != nil && strings.TrimSpace(a.subagent.PromptTemplate) != "" {
		return a.buildTemplatedChildPrompt(mode, toolDefs, clock)
	}
	promptTodoMD := checklistMarkdownFromPlan(a.state.GetPlan())
	// The session notes only: the memory subagent's report is per-turn text
	// and never enters the system message (see above).
	mem := formatSessionNotes(strings.TrimSpace(a.state.GetAgentMemory()))
	planCtx := ""
	if mode == "agent" {
		// Read, never taken. The turn it belongs to renders this prompt more
		// than once - a rebuild after a compaction, the continuation after a
		// permission prompt - and releasePlanContext hands it back when that
		// turn is really over (react.go).
		planCtx = a.state.PendingPlanContext()
	}
	discardedPlans := ""
	if mode == "plan" {
		discardedPlans = discardedPlansPromptBlock(a.state.DiscardedPlanSlugs())
	}
	skillsMD := buildSkillsPromptMarkdown(a.state.GetSkills(), activeSkills, a.cfg.Skills.AutoDiscoveryEnabled())
	toolsMD := tools.FormatDefinitionsForPrompt(toolDefs)
	rulesMD := ""
	// Project docs the rules block already carries: instructions.files names
	// AGENTS.md too, and one system prompt does not need it twice. A template
	// under prompts.dir may render {{.Instructions}} and not {{.Rules}}, and
	// then nothing carries them - so the skip list is taken only from a
	// template that actually prints the block.
	var embeddedDocs []string
	var renderedRules []*rules.Rule
	rendersRules := prompts.RendersRules(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), a.cfg.Prompts.AskFile())
	if rs, ok := a.state.(rulesState); ok {
		rulesMD, embeddedDocs, renderedRules = buildRulesPromptMarkdown(rs, a.cfg.Paths.Home, contextFiles, userText, a.agentsOnDemand())
		if !rendersRules {
			embeddedDocs = nil
		}
	}
	instructionsMD := session.LoadInstructions(a.state.GetCWD(), a.cfg.Paths.Home, a.cfg.Instructions.Files, embeddedDocs)
	full := prompts.RenderWithFallback(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), a.cfg.Prompts.AskFile(), prompts.TemplateData{
		CWD:            a.state.GetCWD(),
		Skills:         skillsMD,
		Rules:          rulesMD,
		Tools:          toolsMD,
		Memory:         mem,
		TodoList:       promptTodoMD,
		PlanContext:    planCtx,
		DiscardedPlans: discardedPlans,
		Instructions:   instructionsMD,
		Subagents:      a.subagentCatalogBlock(),
		SubagentRole:   a.subagentRoleBlock(),
		// The built-in templates no longer render this: a wall clock in the
		// system message breaks the provider's prefix cache on every request,
		// and the turn context block carries the clock instead. It stays
		// available to an operator's own prompts.dir template, at that cost.
		UTCNow: clock.Format(time.RFC3339),
	})
	full = joinNonEmptyPromptBlocks(full, a.environment.PromptContext())
	// Context handed over by SessionStart and UserPromptSubmit hooks; appended
	// like the environment block so a custom template carries it too.
	full = joinNonEmptyPromptBlocks(full, a.hookContextBlock())
	// What the surface running this turn asked the model to know about
	// answering through it. Last of the appended blocks, so a messenger's
	// answer format is the nearest instruction to the conversation itself.
	full = joinNonEmptyPromptBlocks(full, a.surfaceBlock())
	// Applied last so it also covers a user's own prompts.dir template and the
	// render fallback, and before the context breakdown so the estimate counts
	// what is actually sent. See internal/prompts/identity.go.
	full = prompts.WithIdentity(full)
	build := &systemPromptBuild{
		Mode:          mode,
		Content:       full,
		SkillsMD:      skillsMD,
		ToolsMD:       toolsMD,
		RulesMD:       rulesMD,
		ToolDefs:      toolDefs,
		RenderedRules: renderedRules,
		Clock:         clock,
		RendersRules:  rendersRules,
		Volatile:      prompts.RendersVolatile(mode, promptsDir, a.cfg.Prompts.AgentFile(), a.cfg.Prompts.PlanFile(), a.cfg.Prompts.AskFile()),
	}
	a.refreshContextBreakdown(build, "")
	return build
}

// buildTemplatedChildPrompt renders the system prompt of a system child that
// carries a template of its own (the memory subagent): the template with the
// working directory and the tool list, then the environment block, the hook
// context and the identity line as for every prompt. Skills, rules, project
// instructions, the subagent catalog and the session memory are not
// rendered: the child's task is not the workspace's, and a template that
// does not print them must not be handed them through the back door.
func (a *Agent) buildTemplatedChildPrompt(mode string, toolDefs []llm.ToolDefinition, clock time.Time) *systemPromptBuild {
	toolsMD := tools.FormatDefinitionsForPrompt(toolDefs)
	// The template frames the child itself, so the role slot carries the
	// role text alone: for the memory child, the operator's additional_prompt.
	full, err := prompts.RenderSource(a.subagent.Kind, a.subagent.PromptTemplate, prompts.TemplateData{
		CWD:          a.state.GetCWD(),
		Tools:        toolsMD,
		SubagentRole: strings.TrimSpace(a.subagent.Role),
		UTCNow:       clock.Format(time.RFC3339),
	})
	if err != nil {
		a.log.Warn("child prompt template failed to render; using the role alone", "kind", a.subagent.Kind, "error", err)
		full = a.subagentRoleBlock()
	}
	full = joinNonEmptyPromptBlocks(full, a.environment.PromptContext())
	full = joinNonEmptyPromptBlocks(full, a.hookContextBlock())
	full = prompts.WithIdentity(full)
	build := &systemPromptBuild{
		Mode:     mode,
		Content:  full,
		ToolsMD:  toolsMD,
		ToolDefs: toolDefs,
		Clock:    clock,
	}
	a.refreshContextBreakdown(build, "")
	return build
}

// refreshContextBreakdown re-estimates the context UI from a frozen system
// prompt. The loop calls it every step, because the estimate is what
// auto-compaction reads and tool results grow between calls while the system
// message no longer moves. turnCtx is the block that will trail the history, so
// what the request actually costs is counted.
func (a *Agent) refreshContextBreakdown(build *systemPromptBuild, turnCtx string) {
	if build == nil {
		return
	}
	if _, ok := a.state.(rulesState); !ok {
		return
	}
	// The Conversation estimate mirrors what buildMessages sends: only the
	// LLM-visible window after the last compaction summary.
	sys := joinNonEmptyPromptBlocks(build.Content, turnCtx)
	msgs := a.prunedForLLM(session.MessagesForLLM(a.state.GetMessages()))
	a.setContextBreakdown(computeContextBreakdown(sys, build.SkillsMD, build.ToolsMD, build.RulesMD, msgs, build.ToolDefs), false)
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

// formatSessionNotes is the {{.Memory}} slot: the notes of this session, which
// move rarely. The memory subagent's report is not part of it.
func formatSessionNotes(sessionNotes string) string {
	if sessionNotes == "" {
		return ""
	}
	return "Session notes:\n" + sessionNotes
}
