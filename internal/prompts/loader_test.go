package prompts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

// Deterministic clock value for built-in template tests (RFC3339 UTC).
const fixtureUTC = "2038-01-19T03:14:07Z"

const (
	defaultAgentTplFile = "agent.md"
	defaultPlanTplFile  = "plan.md"
	defaultAskTplFile   = "ask.md"
)

func TestRenderAgentPrompt(t *testing.T) {
	result, err := prompts.Render("agent", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/home/user/project",
		UTCNow: fixtureUTC,
	})
	if err != nil {
		t.Fatalf("Render agent: %v", err)
	}
	if !strings.Contains(result, "/home/user/project") {
		t.Error("agent prompt should contain CWD")
	}
	if !strings.Contains(result, "Mode: Agent") {
		t.Error("agent prompt should mention Mode: Agent")
	}
	if strings.Contains(result, "## Available tools") {
		t.Error("tools section should be omitted when .Tools is empty")
	}
	if strings.Contains(result, "### Current todo checklist") {
		t.Error("todo checklist section should be omitted when .TodoList is empty")
	}
	if !strings.Contains(result, "## Current UTC time") || !strings.Contains(result, fixtureUTC) {
		t.Error("agent prompt should end with Current UTC time section")
	}
}

func TestRenderPlanPrompt(t *testing.T) {
	result, err := prompts.Render("plan", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/tmp/workspace",
		UTCNow: fixtureUTC,
	})
	if err != nil {
		t.Fatalf("Render plan: %v", err)
	}
	if !strings.Contains(result, "/tmp/workspace") {
		t.Error("plan prompt should contain CWD")
	}
	if !strings.Contains(result, "Mode: Plan") {
		t.Error("plan prompt should mention Mode: Plan")
	}
	if !strings.Contains(result, "plan_write") {
		t.Error("plan prompt should mention plan_write")
	}
	if !strings.Contains(result, "plan_read") {
		t.Error("plan prompt should mention plan_read")
	}
	if !strings.Contains(result, "websearch") {
		t.Error("plan prompt should mention websearch for external research")
	}
	if !strings.Contains(result, "run_command") {
		t.Error("plan prompt should mention run_command for shell inspection")
	}
	if !strings.Contains(result, "MCP") {
		t.Error("plan prompt should mention MCP servers")
	}
	if !strings.Contains(result, "## Current UTC time") || !strings.Contains(result, fixtureUTC) {
		t.Error("plan prompt should end with Current UTC time section")
	}
}

func TestRenderAskPrompt(t *testing.T) {
	result, err := prompts.Render("ask", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/tmp/workspace",
		UTCNow: fixtureUTC,
	})
	if err != nil {
		t.Fatalf("Render ask: %v", err)
	}
	if !strings.Contains(result, "/tmp/workspace") {
		t.Error("ask prompt should contain CWD")
	}
	if !strings.Contains(result, "Mode: Ask") {
		t.Error("ask prompt should mention Mode: Ask")
	}
	if !strings.Contains(result, "read-only") {
		t.Error("ask prompt should state the read-only boundary")
	}
	if !strings.Contains(result, "websearch") {
		t.Error("ask prompt should mention websearch for external research")
	}
	if strings.Contains(result, "run_command") {
		t.Error("ask prompt should not mention run_command (shell is unavailable)")
	}
	if strings.Contains(result, "plan_write") {
		t.Error("ask prompt should not mention plan_write (plan tools are unavailable)")
	}
	if !strings.Contains(result, "## Current UTC time") || !strings.Contains(result, fixtureUTC) {
		t.Error("ask prompt should end with Current UTC time section")
	}
}

func TestRenderWithSkillsToolsMemory(t *testing.T) {
	result, err := prompts.Render("agent", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/project",
		Skills: "## Active Skills\n\nstub",
		Tools:  "- `read`: read",
		Memory: "User prefers pytest.",
		UTCNow: fixtureUTC,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"stub", "`read`", "pytest", "Session memory"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result", want)
		}
	}
}

func TestRenderEmptyOptionalSections(t *testing.T) {
	result, err := prompts.Render("agent", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/project",
		UTCNow: fixtureUTC,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(result, "## Active Skills") {
		t.Error("should not emit skills heading when Skills data is empty")
	}
	if strings.Contains(result, "## Available tools") {
		t.Error("should not emit tools section when Tools data is empty")
	}
	if strings.Contains(result, "Session memory") {
		t.Error("should not emit memory section when Memory is empty")
	}
	if strings.Contains(result, "### Current todo checklist") {
		t.Error("should not emit todo checklist heading when TodoList is empty")
	}
}

func TestRenderTodoListWhenNonempty(t *testing.T) {
	todoMd := "- [ ] alpha\n- [x] beta"
	a, err := prompts.Render("agent", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p", TodoList: todoMd, UTCNow: fixtureUTC})
	if err != nil {
		t.Fatalf("Render agent: %v", err)
	}
	if !strings.Contains(a, "### Current todo checklist") || !strings.Contains(a, "- [ ] alpha") || !strings.Contains(a, "- [x] beta") {
		t.Errorf("expected injected todo markdown in agent prompt, got excerpt: %.200s", a)
	}

	p, err := prompts.Render("plan", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p", TodoList: todoMd, UTCNow: fixtureUTC})
	if err != nil {
		t.Fatalf("Render plan: %v", err)
	}
	if strings.Contains(p, "### Current todo checklist") {
		t.Error("plan prompt should not include session todo checklist section")
	}
}

func TestRenderUsesCustomTemplateFilenames(t *testing.T) {
	customAgent := "my-agent.tpl"
	body := "Hello {{.CWD}}\n"
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, customAgent), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := prompts.Render("agent", tmp, customAgent, "ignored-plan.tpl", "ignored-ask.tpl", prompts.TemplateData{CWD: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello /x" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderCustomPromptDir(t *testing.T) {
	customContent := "Custom for {{.CWD}}.\nSkills: {{.Skills}}\n"
	tmp := t.TempDir()
	path := filepath.Join(tmp, "agent.md")
	if err := os.WriteFile(path, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := prompts.Render("agent", tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD:    "/my/project",
		Skills: "S1",
	})
	if err != nil {
		t.Fatalf("Render custom dir: %v", err)
	}
	if !strings.Contains(result, "Custom for /my/project") || !strings.Contains(result, "S1") {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestRenderCustomDirMissingAgentFile(t *testing.T) {
	tmp := t.TempDir()
	_, err := prompts.Render("agent", tmp, defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{
		CWD: "/project",
	})
	if err == nil {
		t.Error("expected error when agent.md is missing in prompts dir")
	}
}

func TestRenderUnknownModeFallsBackToAgent(t *testing.T) {
	agent, _ := prompts.Render("agent", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p", UTCNow: fixtureUTC})
	unknown, err := prompts.Render("unknown_mode", "", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p", UTCNow: fixtureUTC})
	if err != nil {
		t.Fatalf("Render unknown mode: %v", err)
	}
	if agent != unknown {
		t.Error("unknown mode should use agent prompt file semantics")
	}
}

func TestDefaultSource(t *testing.T) {
	agentSrc := prompts.DefaultSource("agent")
	if agentSrc == "" {
		t.Error("agent source should not be empty")
	}
	if !strings.Contains(agentSrc, "{{.CWD}}") {
		t.Error("agent source should contain {{.CWD}} template variable")
	}
	if !strings.Contains(agentSrc, "{{.Skills}}") {
		t.Error("agent source should contain {{.Skills}}")
	}
	if !strings.Contains(agentSrc, "{{if .TodoList}}") {
		t.Error("agent source should conditionalize TodoList injection")
	}
	if !strings.Contains(agentSrc, "{{.UTCNow}}") {
		t.Error("agent source should expose UTCNow for clock grounding")
	}

	planSrc := prompts.DefaultSource("plan")
	if planSrc == "" {
		t.Error("plan source should not be empty")
	}
	if planSrc == agentSrc {
		t.Error("plan and agent sources should differ")
	}

	askSrc := prompts.DefaultSource("ask")
	if askSrc == "" {
		t.Error("ask source should not be empty")
	}
	if askSrc == agentSrc || askSrc == planSrc {
		t.Error("ask source should differ from agent and plan sources")
	}
}

func TestRenderWithFallbackNoPanic(t *testing.T) {
	result := prompts.RenderWithFallback("agent", "/nonexistent/prompt-dir", defaultAgentTplFile, defaultPlanTplFile, defaultAskTplFile, prompts.TemplateData{CWD: "/p"})
	if result == "" {
		t.Error("RenderWithFallback should return non-empty string even on error")
	}
}
