//go:build cli

package cli

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Print mode approves under bypass by itself, and says so: the decisions
// check stands in for the person nobody asked.
func TestPrintSenderMarksItsApprovalAutomatic(t *testing.T) {
	p := &printSender{cfg: &config.Config{Tools: config.Tools{PermissionMode: config.PermModeBypass}}}
	res, err := p.RequestPermission(context.Background(), acp.PermissionRequestParams{
		SessionID:             "x",
		SessionPermissionMode: config.PermModeBypass,
	})
	if err != nil || res == nil || res.OptionID != "allow" || !res.Automatic {
		t.Fatalf("result = %+v %v, want an automatic allow", res, err)
	}
}
