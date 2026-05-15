package tools_test

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func TestFormatDefinitionsForPrompt(t *testing.T) {
	out := tools.FormatDefinitionsForPrompt([]llm.ToolDefinition{
		{Name: "read", Description: "Read a file."},
		{Name: "grep", Description: ""},
	})
	for _, want := range []string{"read", "Read a file.", "grep", "(no description)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q should contain %q", out, want)
		}
	}
}

func TestFormatDefinitionsForPromptEmpty(t *testing.T) {
	if tools.FormatDefinitionsForPrompt(nil) != "" {
		t.Fatal("expected empty string")
	}
}
