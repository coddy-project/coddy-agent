//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The gateway cannot prompt anyone, so it auto-allows the parent's own
// requests; a subagent narrowed below bypass, recognisable by the stamped
// effective mode, is denied instead of waved through.
func TestSenderRequestPermissionDeniesANarrowedSubagent(t *testing.T) {
	s := &Sender{}
	params := func(mode string) acp.PermissionRequestParams {
		return acp.PermissionRequestParams{
			SessionID:               "sess_parent",
			ToolCall:                acp.PermissionToolCall{ToolCallID: "c1", Status: "pending"},
			EffectivePermissionMode: mode,
		}
	}
	if got, _ := s.RequestPermission(context.Background(), params("")); got.OptionID != "allow" {
		t.Fatalf("unstamped request = %#v, want allow", got)
	}
	if got, _ := s.RequestPermission(context.Background(), params("bypass")); got.OptionID != "allow" {
		t.Fatalf("stamped bypass = %#v, want allow", got)
	}
	for _, mode := range []string{"ask", "accept_edits"} {
		if got, _ := s.RequestPermission(context.Background(), params(mode)); got.OptionID != "reject" || got.Outcome != "cancelled" {
			t.Fatalf("stamped %s = %#v, want a denial", mode, got)
		}
	}
}
