package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
)

type recordingBroker struct {
	requests []agent.DetachedPermissionRequest
}

func (b *recordingBroker) RequestDetachedPermission(_ context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.requests = append(b.requests, req)
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

// Every turn on the shared manager is handed the runtime itself as its broker,
// long before the HTTP surface that can show a prompt is up - and after it went
// away. The slot has to answer for both moments: an empty slot is "nobody can be
// asked", which the relay turns into a refusal with a reason, and an installed
// broker gets the request unchanged.
func TestRuntimeDetachedPermissionSlot(t *testing.T) {
	rt := &Runtime{}
	req := agent.DetachedPermissionRequest{ChildSessionID: "sess_child", TaskID: "bg_1", AgentName: "writer"}

	if _, err := rt.RequestDetachedPermission(context.Background(), req); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("empty slot answered %v, want ErrNoDetachedApprover", err)
	}

	broker := &recordingBroker{}
	rt.SetDetachedPermissionBroker(broker)
	res, err := rt.RequestDetachedPermission(context.Background(), req)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("installed broker answered %+v, %v; want its allow", res, err)
	}
	if len(broker.requests) != 1 || broker.requests[0].ChildSessionID != "sess_child" || broker.requests[0].TaskID != "bg_1" {
		t.Fatalf("broker saw %+v", broker.requests)
	}

	rt.SetDetachedPermissionBroker(nil)
	if _, err := rt.RequestDetachedPermission(context.Background(), req); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("cleared slot answered %v, want ErrNoDetachedApprover", err)
	}
}
