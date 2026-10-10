//go:build http

package httpserver

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// A plan run approves every prompt by itself, and says so: the decisions
// check stands in for the person nobody asked.
func TestPlanRunSenderMarksItsApprovalAutomatic(t *testing.T) {
	res, err := planRunNoopSender{}.RequestPermission(context.Background(), acp.PermissionRequestParams{SessionID: "x"})
	if err != nil || res == nil || res.OptionID != "allow" || !res.Automatic {
		t.Fatalf("result = %+v %v, want an automatic allow", res, err)
	}
}
