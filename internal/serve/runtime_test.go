package serve

import (
	"context"
	"errors"
	"testing"
	"time"

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

type decliningBroker struct{ err error }

func (b decliningBroker) RequestDetachedPermission(context.Context, agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	return nil, b.err
}

// waitingBroker shows the prompt and waits until the runtime withdraws it.
type waitingBroker struct {
	asked     chan struct{}
	withdrawn chan struct{}
}

func newWaitingBroker() *waitingBroker {
	return &waitingBroker{asked: make(chan struct{}, 1), withdrawn: make(chan struct{}, 1)}
}

func (b *waitingBroker) RequestDetachedPermission(ctx context.Context, _ agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.asked <- struct{}{}
	<-ctx.Done()
	b.withdrawn <- struct{}{}
	return nil, nil
}

var detachedReq = agent.DetachedPermissionRequest{ParentSessionID: "sess_parent", ChildSessionID: "sess_child", TaskID: "bg_1", AgentName: "writer"}

// Every turn on the shared manager is handed the runtime itself as its broker,
// long before a surface that can show a prompt is up - and after it went away.
// No surface at all is "nobody can be asked", which the relay turns into a
// refusal with a reason; an offered surface gets the request unchanged.
func TestRuntimeDetachedPermissionSurfaces(t *testing.T) {
	rt := &Runtime{}

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("no surface answered %v, want ErrNoDetachedApprover", err)
	}

	broker := &recordingBroker{}
	withdraw := rt.AddDetachedPermissionApprover(broker)
	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("offered surface answered %+v, %v; want its allow", res, err)
	}
	if len(broker.requests) != 1 || broker.requests[0].ChildSessionID != "sess_child" || broker.requests[0].TaskID != "bg_1" {
		t.Fatalf("surface saw %+v", broker.requests)
	}

	withdraw()
	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("withdrawn surface answered %v, want ErrNoDetachedApprover", err)
	}
}

// A surface restarted under a settings change can come up before the old one
// has finished stopping, so withdrawing the old offer must not take the new one
// with it.
func TestRuntimeWithdrawingAnOfferLeavesTheOthers(t *testing.T) {
	rt := &Runtime{}
	withdrawOld := rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	fresh := &recordingBroker{}
	rt.AddDetachedPermissionApprover(fresh)

	withdrawOld()
	withdrawOld()

	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("after withdrawing the old offer got %+v, %v; want the fresh surface's allow", res, err)
	}
	if rt.AddDetachedPermissionApprover(nil) == nil {
		t.Fatal("offering nil must still return a callable withdraw")
	}
}

// When every surface says it cannot show this prompt - or failed to - nobody was
// asked, and the subagent must hear exactly that rather than "unanswered".
func TestRuntimeDetachedPermissionEverySurfaceDeclines(t *testing.T) {
	rt := &Runtime{}
	rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	rt.AddDetachedPermissionApprover(decliningBroker{err: errors.New("telegram: send failed")})

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("every surface declined but got %v, want ErrNoDetachedApprover", err)
	}
}

// The wait ends with the run: a stopped or timed-out child cancels the context,
// and every surface that shows the prompt must be told so it can take it down.
func TestRuntimeDetachedPermissionCancelReachesEverySurface(t *testing.T) {
	rt := &Runtime{}
	web, chat := newWaitingBroker(), newWaitingBroker()
	rt.AddDetachedPermissionApprover(web)
	rt.AddDetachedPermissionApprover(chat)

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *acp.PermissionResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := rt.RequestDetachedPermission(ctx, detachedReq)
		done <- outcome{res, err}
	}()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.asked:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface was never asked")
		}
	}
	cancel()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.withdrawn:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface kept the prompt after the run ended")
		}
	}
	select {
	case out := <-done:
		if out.res != nil || out.err != nil {
			t.Fatalf("cancelled wait answered %+v, %v; want nil, nil", out.res, out.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime never returned after the run ended")
	}
}
