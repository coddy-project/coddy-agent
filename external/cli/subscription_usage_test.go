//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestSubscriptionUsagePresentation(t *testing.T) {
	for _, kind := range []string{"codex", "devin"} {
		t.Run(kind, func(t *testing.T) {
			u := &acp.ProviderUsageUpdate{Provider: "work", ProviderType: kind, Plan: "pro", Windows: []acp.UsageWindow{{ID: "day", Label: "day", UsedPercent: 0}}}
			wantBrand := map[string]string{"codex": "Codex", "devin": "Devin"}[kind]
			if usageBrand(u) != wantBrand {
				t.Errorf("brand = %s, want %s", usageBrand(u), wantBrand)
			}
			now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
			var pieces []string
			for _, s := range usageFooterSegments(u, "work/model", now) {
				pieces = append(pieces, s.text)
			}
			if !strings.Contains(strings.Join(pieces, " "), "day 0%") {
				t.Errorf("zero daily quota hidden: %v", pieces)
			}
			report := strings.Join(usageReportLines(u, "work/model", now), "\n")
			if strings.Contains(report, "cooldown") || strings.Contains(report, "rpm") || strings.Contains(report, " / ") {
				t.Errorf("invented limits: %s", report)
			}
			u.Windows = nil
			for _, errKind := range []string{"", "unavailable"} {
				u.Error = errKind
				pieces = nil
				for _, s := range usageFooterSegments(u, "work/model", now) {
					pieces = append(pieces, s.text)
				}
				if !strings.Contains(strings.Join(pieces, " "), "quota unavailable") {
					t.Errorf("missing data not explained: %v", pieces)
				}
				report = strings.Join(usageReportLines(u, "work/model", now), "\n")
				if !strings.Contains(report, "quota unavailable") || strings.Contains(report, "∞") || strings.Contains(report, "cooldown") {
					t.Errorf("unavailable report: %s", report)
				}
			}
		})
	}
}
