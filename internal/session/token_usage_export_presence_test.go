package session

import "testing"

func TestExportTokenUsagePresence(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name    string
		usage   TokenUsageTotals
		present bool
	}{
		{"unknown", TokenUsageTotals{UsageKnown: &no, InputTokens: 9, TotalTokens: 9}, false},
		{"zero", TokenUsageTotals{UsageKnown: &yes}, true},
		{"legacy nonzero", TokenUsageTotals{InputTokens: 9, TotalTokens: 9}, true},
		{"legacy zero", TokenUsageTotals{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildExportDocument(ExportInput{Stats: &SessionStats{TokenUsageTotal: tc.usage}}).Session.TokenUsage
			if (got != nil) != tc.present {
				t.Fatalf("usage=%+v want present %v", got, tc.present)
			}
		})
	}
}
