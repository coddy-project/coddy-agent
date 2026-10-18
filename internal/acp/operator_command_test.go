package acp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitDecision(t *testing.T, tk *OperatorCommandTicket) *OperatorCommandDecision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := tk.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return d
}

func TestOperatorCommandTicketRunThenStopThenKill(t *testing.T) {
	tk := NewOperatorCommandTicket()
	if err := tk.Answer(OperatorActionStop, ""); !errors.Is(err, ErrOperatorCommandConflict) {
		t.Fatalf("stop before run: err = %v, want a conflict", err)
	}
	if err := tk.Answer(OperatorActionRun, ""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := tk.Answer(OperatorActionRun, ""); !errors.Is(err, ErrOperatorCommandConflict) {
		t.Fatalf("second run: err = %v, want a conflict", err)
	}
	d := waitDecision(t, tk)
	if d.Action != OperatorActionRun {
		t.Fatalf("decision = %q", d.Action)
	}
	select {
	case <-d.Stop:
		t.Fatal("stop closed before any stop answer")
	default:
	}
	if err := tk.Answer(OperatorActionStop, ""); err != nil {
		t.Fatalf("stop: %v", err)
	}
	<-d.Stop
	select {
	case <-d.Kill:
		t.Fatal("kill closed by the first stop")
	default:
	}
	if err := tk.Answer(OperatorActionStop, ""); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	<-d.Kill
	d.Done()
	if err := tk.Answer(OperatorActionStop, ""); !errors.Is(err, ErrOperatorCommandEnded) {
		t.Fatalf("stop after the end: err = %v, want ended", err)
	}
	if got := tk.State(); got != OperatorStateEnded {
		t.Fatalf("state = %q", got)
	}
}

func TestOperatorCommandTicketDecline(t *testing.T) {
	tk := NewOperatorCommandTicket()
	if err := tk.Answer(OperatorActionDecline, "not on prod"); err != nil {
		t.Fatal(err)
	}
	d := waitDecision(t, tk)
	if d.Action != OperatorActionDecline || d.Note != "not on prod" {
		t.Fatalf("decision = %+v", d)
	}
	for _, a := range []string{OperatorActionRun, OperatorActionStop, OperatorActionDecline} {
		if err := tk.Answer(a, ""); err == nil {
			t.Fatalf("%s after decline was accepted", a)
		}
	}
}

func TestOperatorCommandTicketRejectsAnUnknownAction(t *testing.T) {
	if err := NewOperatorCommandTicket().Answer("edit", ""); !errors.Is(err, ErrOperatorCommandAction) {
		t.Fatalf("err = %v", err)
	}
}

// TestOperatorCommandTicketFirstAnswerWins: two followers press at once, one
// decision comes out and the other answer is a conflict.
func TestOperatorCommandTicketFirstAnswerWins(t *testing.T) {
	for i := 0; i < 50; i++ {
		tk := NewOperatorCommandTicket()
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, a := range []string{OperatorActionRun, OperatorActionDecline} {
			wg.Add(1)
			go func(j int, a string) {
				defer wg.Done()
				errs[j] = tk.Answer(a, "")
			}(j, a)
		}
		wg.Wait()
		if (errs[0] == nil) == (errs[1] == nil) {
			t.Fatalf("both or neither answer won: %v", errs)
		}
		d := waitDecision(t, tk)
		if (errs[0] == nil && d.Action != OperatorActionRun) || (errs[1] == nil && d.Action != OperatorActionDecline) {
			t.Fatalf("the winning answer is not the decision: %v, %+v", errs, d)
		}
	}
}

func TestOperatorCommandTicketWaitEndsWithTheContext(t *testing.T) {
	tk := NewOperatorCommandTicket()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tk.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := tk.Answer(OperatorActionRun, ""); !errors.Is(err, ErrOperatorCommandEnded) {
		t.Fatalf("run after a cancelled wait: err = %v", err)
	}
}

type plainSender struct{}

func (plainSender) SendSessionUpdate(string, interface{}) error { return nil }
func (plainSender) RequestPermission(context.Context, PermissionRequestParams) (*PermissionResult, error) {
	return nil, nil
}
func (plainSender) RequestQuestion(context.Context, QuestionRequestParams) (*QuestionResult, error) {
	return nil, nil
}

type operatorSender struct {
	plainSender
	available bool
}

func (s operatorSender) OperatorCommandsAvailable() bool { return s.available }
func (operatorSender) RequestOperatorCommand(context.Context, OperatorCommandRequest) (*OperatorCommandDecision, error) {
	return nil, nil
}

type wrapper struct {
	UpdateSender
	forward bool
}

func (w wrapper) UnwrapSender() UpdateSender {
	if !w.forward {
		return nil
	}
	return w.UpdateSender
}

func TestOperatorCommandsOf(t *testing.T) {
	if s := OperatorCommandsOf(nil); s != nil {
		t.Fatal("nil sender")
	}
	if s := OperatorCommandsOf(plainSender{}); s != nil {
		t.Fatal("a sender without the capability")
	}
	if s := OperatorCommandsOf(operatorSender{available: false}); s != nil {
		t.Fatal("a sender that reports nobody can answer")
	}
	if s := OperatorCommandsOf(operatorSender{available: true}); s == nil {
		t.Fatal("a sender with a person")
	}
	if s := OperatorCommandsOf(wrapper{UpdateSender: operatorSender{available: true}, forward: true}); s == nil {
		t.Fatal("a forwarding wrapper hid the capability")
	}
	if s := OperatorCommandsOf(wrapper{UpdateSender: operatorSender{available: true}, forward: false}); s != nil {
		t.Fatal("a denying wrapper let the capability through")
	}
	// An embedding wrapper that says nothing denies: the default is no.
	type silent struct{ UpdateSender }
	if s := OperatorCommandsOf(silent{operatorSender{available: true}}); s != nil {
		t.Fatal("a silent wrapper let the capability through")
	}
}

// TestOperatorCommandTicketWaitAfterTheEnd: a second Wait on a request that is
// over is refused rather than read as a run.
func TestOperatorCommandTicketWaitAfterTheEnd(t *testing.T) {
	tk := NewOperatorCommandTicket()
	_ = tk.Answer(OperatorActionRun, "")
	d := waitDecision(t, tk)
	d.Done()
	if _, err := tk.Wait(context.Background()); !errors.Is(err, ErrOperatorCommandEnded) {
		t.Fatalf("err = %v, want ended", err)
	}
}
