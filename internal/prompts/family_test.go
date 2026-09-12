package prompts_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

func TestFamily(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{"anthropic by model", "", "claude-3-5-sonnet", "anthropic"},
		{"anthropic by opus id", "anthropic", "claude-opus-4-8", "anthropic"},
		{"anthropic by provider only", "anthropic", "", "anthropic"},
		{"openai gpt-4o", "openai", "gpt-4o", "openai"},
		{"openai o3-mini", "openai", "o3-mini", "openai"},
		{"openai o1 by model only", "", "o1-preview", "openai"},
		{"openai by provider only", "openai", "some-tuned", "openai"},
		{"codex provider", "codex", "gpt-5.3-codex", "openai"},
		{"gemini", "", "gemini-2.5-pro", "gemini"},
		{"gemma", "", "gemma-4-27b-it", "gemma"},
		{"gemma 4 via neuraldeep", "neuraldeep", "gemma-4-31b", "gemma"},
		{"gemma 4 noreason via neuraldeep", "neuraldeep", "gemma-4-31b-noreason", "gemma"},
		{"qwen with space", "neuraldeep", "qwen 3.6", "qwen"},
		{"qwen via neuraldeep", "neuraldeep", "qwen3.6-35b-a3b-noreason", "qwen"},
		{"gpt-oss beats openai provider", "openai", "gpt-oss-120b", "gpt-oss"},
		{"gpt-oss via neuraldeep", "neuraldeep", "gpt-oss-120b", "gpt-oss"},
		{"neuraldeep local fallback", "neuraldeep", "kimi-k2.6", "neuraldeep"},
		{"case-insensitive", "", "Claude-Sonnet", "anthropic"},
		{"empty is fallback", "", "", ""},
		{"unknown is fallback", "unknown", "mistral-large-latest", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prompts.Family(tc.provider, tc.model); got != tc.want {
				t.Errorf("Family(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}
