package prompts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

func TestRenderAgentPrompt(t *testing.T) {
	result, err := prompts.Render("agent", "", prompts.TemplateData{
		CWD: "/home/user/project",
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
}

func TestRenderPlanPrompt(t *testing.T) {
	result, err := prompts.Render("plan", "", prompts.TemplateData{
		CWD: "/tmp/workspace",
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
	if !strings.Contains(result, "switch_to_agent_mode") {
		t.Error("plan prompt should mention switch_to_agent_mode tool")
	}
}

func TestRenderWithExtraInstructions(t *testing.T) {
	result, err := prompts.Render("agent", "", prompts.TemplateData{
		CWD:               "/project",
		ExtraInstructions: "Always use tabs for indentation.",
	})
	if err != nil {
		t.Fatalf("Render with extra: %v", err)
	}
	if !strings.Contains(result, "Always use tabs for indentation.") {
		t.Error("should contain extra instructions")
	}
}

func TestRenderEmptyExtraInstructions(t *testing.T) {
	result, err := prompts.Render("agent", "", prompts.TemplateData{
		CWD:               "/project",
		ExtraInstructions: "",
	})
	if err != nil {
		t.Fatalf("Render empty extra: %v", err)
	}
	if strings.Contains(result, "Additional instructions") {
		t.Error("should not contain 'Additional instructions' section when extra is empty")
	}
}

func TestRenderCustomFile(t *testing.T) {
	customContent := "Custom prompt for {{.CWD}}. Mode: custom."
	tmp := t.TempDir()
	path := filepath.Join(tmp, "custom.md")
	if err := os.WriteFile(path, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := prompts.Render("agent", path, prompts.TemplateData{
		CWD: "/my/project",
	})
	if err != nil {
		t.Fatalf("Render custom file: %v", err)
	}
	if !strings.Contains(result, "Custom prompt for /my/project") {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestRenderCustomFileMissing(t *testing.T) {
	_, err := prompts.Render("agent", "/nonexistent/prompt.md", prompts.TemplateData{
		CWD: "/project",
	})
	if err == nil {
		t.Error("expected error for missing custom file")
	}
}

func TestRenderUnknownModeFallsBackToAgent(t *testing.T) {
	agent, _ := prompts.Render("agent", "", prompts.TemplateData{CWD: "/p"})
	unknown, err := prompts.Render("unknown_mode", "", prompts.TemplateData{CWD: "/p"})
	if err != nil {
		t.Fatalf("Render unknown mode: %v", err)
	}
	if agent != unknown {
		t.Error("unknown mode should fall back to agent prompt")
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

	planSrc := prompts.DefaultSource("plan")
	if planSrc == "" {
		t.Error("plan source should not be empty")
	}
	if planSrc == agentSrc {
		t.Error("plan and agent sources should differ")
	}
}

func TestRenderWithFallbackNoPanic(t *testing.T) {
	// Should never panic, even with broken custom file.
	result := prompts.RenderWithFallback("agent", "/nonexistent.md", prompts.TemplateData{CWD: "/p"})
	if result == "" {
		t.Error("RenderWithFallback should return non-empty string even on error")
	}
}
