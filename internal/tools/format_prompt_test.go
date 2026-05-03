package tools_test

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func TestFormatDefinitionsForPrompt(t *testing.T) {
	out := tools.FormatDefinitionsForPrompt([]llm.ToolDefinition{
		{Name: "read_file", Description: "Read a file."},
		{Name: "list_dir", Description: ""},
	})
	for _, want := range []string{"read_file", "Read a file.", "list_dir", "(no description)"} {
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
