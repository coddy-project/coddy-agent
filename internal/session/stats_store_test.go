package session

import "testing"

func TestWriteSessionContextBreakdownPreservesTokenTotals(t *testing.T) {
	dir := t.TempDir()
	before := SessionStats{
		TokenUsageTotal: TokenUsageTotals{
			InputTokens:  123,
			OutputTokens: 45,
			TotalTokens:  168,
		},
		TokenUsageByTurn: []TokenUsageTurn{{
			TurnIndex:    2,
			InputTokens:  123,
			OutputTokens: 45,
			TotalTokens:  168,
		}},
		ContextBreakdown: &ContextBreakdown{
			SystemPrompt:   100,
			Conversation:   900,
			EstimatedTotal: 1000,
		},
	}
	if err := WriteSessionStats(dir, before); err != nil {
		t.Fatal(err)
	}

	afterBreakdown := &ContextBreakdown{
		SystemPrompt:   100,
		Conversation:   300,
		EstimatedTotal: 400,
	}
	if err := WriteSessionContextBreakdown(dir, afterBreakdown); err != nil {
		t.Fatal(err)
	}

	got, err := ReadSessionStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenUsageTotal != before.TokenUsageTotal {
		t.Fatalf("token totals changed: got %+v, want %+v", got.TokenUsageTotal, before.TokenUsageTotal)
	}
	if len(got.TokenUsageByTurn) != 1 || got.TokenUsageByTurn[0].TurnIndex != 2 {
		t.Fatalf("per-turn usage changed: %+v", got.TokenUsageByTurn)
	}
	if got.ContextBreakdown == nil || got.ContextBreakdown.EstimatedTotal != 400 {
		t.Fatalf("context breakdown not updated: %+v", got.ContextBreakdown)
	}
}
