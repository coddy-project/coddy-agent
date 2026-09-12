package prompts

import "strings"

// Family classifies an LLM by its provider type and model id into a prompt "family"
// slug used to select model-tuned prompt fragments (notes_<family>.md) and custom
// prompt files (agent.<family>.md).
//
// Model-id patterns take precedence over the provider type because open models
// (qwen/gemma/gpt-oss) are frequently served through OpenAI-compatible or neuraldeep
// endpoints, so the provider type alone would misclassify them.
//
// Returns "" when no family matches; callers fall back to the shared base prompt.
func Family(providerType, modelID string) string {
	p := strings.ToLower(strings.TrimSpace(providerType))
	m := strings.ToLower(strings.TrimSpace(modelID))

	switch {
	case strings.Contains(m, "gpt-oss"):
		return "gpt-oss"
	case strings.Contains(m, "gemini"):
		return "gemini"
	case strings.Contains(m, "gemma"):
		return "gemma"
	case strings.Contains(m, "qwen"):
		return "qwen"
	case strings.Contains(m, "claude") || p == "anthropic":
		return "anthropic"
	case isOpenAIModel(m) || p == "openai" || p == "codex":
		return "openai"
	case p == "neuraldeep":
		return "neuraldeep"
	default:
		return ""
	}
}

// isOpenAIModel reports whether a (lowercased) model id looks like an OpenAI model.
// gpt-oss is handled by the caller before this check, so a plain "gpt" match is safe.
func isOpenAIModel(m string) bool {
	if strings.Contains(m, "gpt") {
		return true
	}
	for _, pfx := range []string{"o1", "o3", "o4"} {
		if strings.HasPrefix(m, pfx) {
			return true
		}
	}
	return false
}
