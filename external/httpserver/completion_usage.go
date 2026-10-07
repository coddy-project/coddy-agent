//go:build http

package httpserver

import "github.com/EvilFreelancer/coddy-agent/internal/llm"

// completionUsage holds provider-reported counters for one HTTP request.
type completionUsage struct {
	input  int
	output int
	cached int
}

func completionUsageFromResponse(response *llm.Response) completionUsage {
	if response == nil {
		return completionUsage{}
	}
	return completionUsage{input: response.InputTokens, output: response.OutputTokens, cached: response.CachedInputTokens}
}

func (u completionUsage) openAI() map[string]interface{} {
	if u.input <= 0 && u.output <= 0 {
		return nil
	}
	usage := map[string]interface{}{
		"prompt_tokens":     u.input,
		"completion_tokens": u.output,
		"total_tokens":      u.input + u.output,
	}
	if u.cached > 0 {
		usage["prompt_tokens_details"] = map[string]int{"cached_tokens": u.cached}
	}
	return usage
}
