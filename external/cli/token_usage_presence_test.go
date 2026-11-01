//go:build cli

package cli

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestFooterIgnoresUnknownUsage(t *testing.T) {
	f := newFooter(newTheme("dark"), t.TempDir())
	a := &App{foot: f}
	known := false
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{UsageKnown: &known, InputTokens: 12, OutputTokens: 4}})
	if f.tokensIn != 0 || f.tokensOut != 0 {
		t.Fatalf("unknown usage counted: %d/%d", f.tokensIn, f.tokensOut)
	}
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{InputTokens: 12, OutputTokens: 4}})
	if f.tokensIn != 12 || f.tokensOut != 4 {
		t.Fatalf("legacy usage lost: %d/%d", f.tokensIn, f.tokensOut)
	}
}

// DESIGN.md (Token usage HUD): an explicitly known zero remains a value and
// is shown as zero; only an unknown total hides the counters.
func TestFooterShowsKnownZeroUsage(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	a := &App{foot: f}
	known := true
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{UsageKnown: &known}})
	second := plain(f.Render(100)[1])
	if !strings.Contains(second, "↑0 ↓0") {
		t.Fatalf("a known zero usage must show as zero, got %q", second)
	}
}

// A legacy update without the usageKnown flag is treated as known, so its
// zero counts show too.
func TestFooterShowsLegacyZeroUsage(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	a := &App{foot: f}
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{}})
	second := plain(f.Render(100)[1])
	if !strings.Contains(second, "↑0 ↓0") {
		t.Fatalf("a legacy update is known and its zero must show, got %q", second)
	}
}

// Before any usage update the counters are not a known zero: nothing has
// been reported yet, so the footer shows no counters.
func TestFooterHidesUsageBeforeFirstUpdate(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	f.SetContext(1.5, 100000)
	second := plain(f.Render(100)[1])
	if strings.Contains(second, "↑") {
		t.Fatalf("no usage was reported yet, the counters must stay hidden, got %q", second)
	}
}

// An unknown update hides the counters for the session; ResetTokens (a new
// or switched session) clears the mark so the next known update shows again,
// at zero included.
func TestFooterResetTokensRestoresKnownDisplay(t *testing.T) {
	f := newFooter(newTheme("dark"), "/work")
	a := &App{foot: f}
	unknown := false
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{UsageKnown: &unknown, InputTokens: 12, OutputTokens: 4}})
	if second := plain(f.Render(100)[1]); strings.Contains(second, "↑") {
		t.Fatalf("an unknown update must hide the counters, got %q", second)
	}
	f.ResetTokens()
	known := true
	a.applyLoopMessage(updateMsg{update: acp.TokenUsageUpdate{UsageKnown: &known}})
	if second := plain(f.Render(100)[1]); !strings.Contains(second, "↑0 ↓0") {
		t.Fatalf("after a reset a known update must show again, got %q", second)
	}
}
