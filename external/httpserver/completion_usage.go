//go:build http

package httpserver

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// completionUsage holds provider-reported counters for one HTTP request.
type completionUsage struct {
	input      int
	output     int
	cached     int
	known      bool
	incomplete bool
}

func completionUsageFromResponse(response *llm.Response) completionUsage {
	if response == nil {
		return completionUsage{}
	}
	return completionUsage{input: response.InputTokens, output: response.OutputTokens, cached: response.CachedInputTokens, known: response.UsageKnown || (!response.UsageIncomplete && (response.InputTokens > 0 || response.OutputTokens > 0))}
}

func (u *completionUsage) add(update acp.TokenUsageUpdate) {
	known := update.InputTokens > 0 || update.OutputTokens > 0
	if update.UsageKnown != nil {
		known = *update.UsageKnown
	}
	if !known {
		u.incomplete = true
		return
	}
	u.known = true
	u.input += update.InputTokens
	u.output += update.OutputTokens
	u.cached += update.CachedInputTokens
}

func (u completionUsage) openAI() map[string]interface{} {
	if !u.known || u.incomplete {
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
