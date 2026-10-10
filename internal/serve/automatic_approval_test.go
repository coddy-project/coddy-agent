package serve

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The sender of a turn no surface carries approves under bypass by itself,
// and says so: the decisions check stands in for the person nobody asked.
func TestDefaultSenderMarksItsApprovalAutomatic(t *testing.T) {
	cfg := &config.Config{Tools: config.Tools{PermissionMode: config.PermModeBypass}}
	s := &defaultSender{live: func() *config.Config { return cfg }}
	res, err := s.RequestPermission(context.Background(), acp.PermissionRequestParams{SessionID: "x"})
	if err != nil || res == nil || res.OptionID != "allow" || !res.Automatic {
		t.Fatalf("result = %+v %v, want an automatic allow", res, err)
	}
}
