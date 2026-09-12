package prompts_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

func TestModelSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"openai/gpt-4o", "openai-gpt-4o"},
		{"anthropic/claude-opus-4-8", "anthropic-claude-opus-4-8"},
		{"neuraldeep/qwen 3.6", "neuraldeep-qwen-3.6"},
		{"neuraldeep/gemma-4-31b-noreason", "neuraldeep-gemma-4-31b-noreason"},
		{"Vendor/Model:Latest", "vendor-model-latest"},
		{"  spaced/name  ", "spaced-name"},
		{"", ""},
		{"///", ""},
		{"gemma-4-27b-it", "gemma-4-27b-it"},
	}
	for _, tc := range cases {
		if got := prompts.ModelSlug(tc.in); got != tc.want {
			t.Errorf("ModelSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
