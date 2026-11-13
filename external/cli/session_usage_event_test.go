//go:build cli

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestFooterHidesUnknownSessionWithKnownCall(t *testing.T) {
	var u acp.TokenUsageUpdate
	if err := json.Unmarshal([]byte(`{"usageKnown":true,"sessionUsageKnown":false,"inputTokens":3,"outputTokens":2}`), &u); err != nil {
		t.Fatal(err)
	}
	f := newFooter(newTheme("dark"), t.TempDir())
	a := &App{foot: f}
	a.applyLoopMessage(updateMsg{update: u})
	if line := plain(f.Render(100)[1]); strings.Contains(line, "↑") {
		t.Fatalf("unknown session shown: %s", line)
	}
}
