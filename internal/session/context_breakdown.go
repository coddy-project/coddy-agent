package session

import "unicode/utf8"

// ContextBreakdown estimates token usage by prompt category.
type ContextBreakdown struct {
	SystemPrompt    int `json:"systemPrompt"`
	ToolDefinitions int `json:"toolDefinitions"`
	Rules           int `json:"rules"`
	Skills          int `json:"skills"`
	MCP             int `json:"mcp"`
	Subagents       int `json:"subagents"`
	Conversation    int `json:"conversation"`
	EstimatedTotal  int `json:"estimatedTotal"`
	// ProviderInputTokens anchors the last provider-reported prompt size to the
	// estimate taken immediately before that request. Later additions are
	// estimated relative to that point, including across session reloads.
	ProviderInputTokens    int    `json:"providerInputTokens,omitempty"`
	ProviderEstimateTokens int    `json:"providerEstimateTokens,omitempty"`
	ProviderModel          string `json:"providerModel,omitempty"`
}

// EstimateTokens approximates tokens for existing bounded compaction requests.
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// EstimateContextTokens is deliberately conservative for the automatic
// trigger. Tokenizers often spend about one token per non-ASCII rune, and
// code needs more room than the four-runes-per-token prose heuristic.
func EstimateContextTokens(s string) int {
	if s == "" {
		return 0
	}
	return EstimateFromCounts(TextCounts(s))
}

// TextCounts counts the ASCII runes of s and the others, an invalid byte
// counting as one other rune, as ranging over s does. The counts of pieces
// add up to the counts of the pieces joined, which lets a caller estimate a
// long text without building it.
func TextCounts(s string) (ascii, other int) {
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			ascii++
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		other++
		i += size
	}
	return ascii, other
}

// EstimateFromCounts is EstimateContextTokens for a text whose runes were
// counted with TextCounts. ASCII prose is near four bytes per token; code is
// denser and non-ASCII text (especially Cyrillic) is often around one token
// per rune.
func EstimateFromCounts(ascii, other int) int {
	if ascii == 0 && other == 0 {
		return 0
	}
	return (ascii+2)/3 + other
}

// Sum sets EstimatedTotal from parts.
func (b *ContextBreakdown) Sum() {
	if b == nil {
		return
	}
	b.EstimatedTotal = b.SystemPrompt + b.ToolDefinitions + b.Rules + b.Skills +
		b.MCP + b.Subagents + b.Conversation
}
