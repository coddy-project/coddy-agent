package session

import "unicode/utf8"

// ContextBreakdown estimates token usage by prompt category (v1: runes/4).
type ContextBreakdown struct {
	SystemPrompt     int `json:"systemPrompt"`
	ToolDefinitions  int `json:"toolDefinitions"`
	Rules            int `json:"rules"`
	Skills           int `json:"skills"`
	MCP              int `json:"mcp"`
	Subagents        int `json:"subagents"`
	Conversation     int `json:"conversation"`
	EstimatedTotal   int `json:"estimatedTotal"`
}

// EstimateTokens approximates tokens from text length.
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// Sum sets EstimatedTotal from parts.
func (b *ContextBreakdown) Sum() {
	if b == nil {
		return
	}
	b.EstimatedTotal = b.SystemPrompt + b.ToolDefinitions + b.Rules + b.Skills +
		b.MCP + b.Subagents + b.Conversation
}
