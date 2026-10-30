package session_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func queueInterrupter(t *testing.T, m *session.Manager) interface{ TryInterruptQueuedTurn(string) bool } {
	t.Helper()
	api, ok := any(m).(interface{ TryInterruptQueuedTurn(string) bool })
	if !ok {
		t.Fatal("manager cannot interrupt a run while preserving its queued messages")
	}
	return api
}

func awaitInterruptSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach the expected checkpoint")
	}
}

func TestQueuedInterruptContinuesUnderTheSameAdmission(t *testing.T) {
	started, unwinding, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	runs := 0
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		runs++
		if runs == 1 {
			close(started)
			<-ctx.Done()
			close(unwinding)
			<-release
			return "", ctx.Err()
		}
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			t.Error("queue continuation inherited full cancellation")
		}
		rows, ok := session.TakeQueuedPromptBatch(ctx, st.GetID())
		if !ok || len(rows) != 2 || rows[0].Text != "first" || rows[1].Text != "second" {
			t.Errorf("FIFO batch = %+v, claimed=%v", rows, ok)
		}
		if !session.BeginQueuedPromptRead(ctx, st.GetID()) {
			t.Error("could not commit the batch")
		}
		return string(acp.StopReasonEndTurn), nil
	}
	m, sid := newQueueManagerWith(t, runner)
	api := queueInterrupter(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
		done <- err
	}()
	awaitInterruptSignal(t, started)
	if _, _, err := m.EnqueueTurnMessageWithMode(sid, "first", session.QueueModeAfterTurn, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.EnqueueTurnMessage(sid, "second"); err != nil {
		t.Fatal(err)
	}
	if !api.TryInterruptQueuedTurn(sid) {
		t.Fatal("queued interrupt was refused")
	}
	awaitInterruptSignal(t, unwinding)
	if !m.SessionTurnActiveInProcess(sid) || ctx.Err() != nil {
		t.Fatal("interrupt released the admission or cancelled its parent")
	}
	if api.TryInterruptQueuedTurn(sid) {
		t.Fatal("a second Stop must fall back to full cancellation")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("continued turn did not finish")
	}
	if runs != 2 {
		t.Fatalf("ran %d times, want the interrupted run and one continuation", runs)
	}
}

func TestQueuedInterruptHardCancelRetainsUnreadFIFO(t *testing.T) {
	for _, fullStop := range []bool{false, true} {
		t.Run(fmt.Sprintf("fullStop=%v", fullStop), func(t *testing.T) {
			started, unwind, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			runs := 0
			runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
				runs++
				close(started)
				<-ctx.Done()
				close(unwind)
				<-release
				if got := st.TakeQueuedMessages(); len(got) != 0 {
					t.Errorf("old runner took post-cutoff rows: %+v", got)
				}
				return string(acp.StopReasonCancelled), nil
			}
			m, sid := newQueueManagerWith(t, runner)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
				done <- err
			}()
			awaitInterruptSignal(t, started)
			var originals []session.QueuedMessage
			for _, mode := range []session.QueueMode{session.QueueModeAfterTurn, session.QueueModeSteer} {
				q, _, err := m.EnqueueTurnMessageWithMode(sid, "duplicate", mode, []acp.ImagePartRef{{Name: "image.png", DataURL: "data:image/png;base64,YQ=="}})
				if err != nil {
					t.Fatal(err)
				}
				originals = append(originals, q)
			}
			if !m.TryInterruptQueuedTurn(sid) {
				t.Fatal("interrupt refused")
			}
			awaitInterruptSignal(t, unwind)
			later, _, err := m.EnqueueTurnMessageWithMode(sid, "later", session.QueueModeAfterTurn, nil)
			if err != nil {
				t.Fatal(err)
			}
			originals = append(originals, later)
			if m.TryInterruptQueuedTurn(sid) {
				t.Fatal("pending batch accepted another interrupt")
			}
			if fullStop {
				m.HandleSessionCancel(acp.SessionCancelParams{SessionID: sid})
			} else {
				cancel()
			}
			close(release)
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not settle")
			}
			if runs != 1 {
				t.Fatalf("hard cancellation restarted the runner %d times", runs)
			}
			st := m.SessionByID(sid)
			if st.MessageQueueOpen() {
				t.Fatal("cancelled queue stayed open")
			}
			for repeat := 0; repeat < 2; repeat++ {
				got := st.QueuedMessages()
				if !reflect.DeepEqual(got, originals) {
					t.Fatalf("retained rows = %+v, want original FIFO %+v", got, originals)
				}
				st.CloseMessageQueue()
			}
		})
	}
}

func TestQueuedInterruptTransfersUnreadBoundaryInput(t *testing.T) {
	boundary, release := make(chan struct{}), make(chan struct{})
	var m *session.Manager
	var sid string
	runs := 0
	var expected []session.QueuedMessage
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		runs++
		switch runs {
		case 1:
			q, _, err := m.EnqueueTurnMessageWithMode(sid, "older", session.QueueModeAfterTurn, nil)
			if err != nil {
				t.Error(err)
			}
			expected = append(expected, q)
		case 2:
			close(boundary)
			<-ctx.Done()
			<-release
			if session.BeginQueuedPromptRead(ctx, sid) {
				t.Error("transferred input could still be appended by the old runner")
			}
			return "", ctx.Err()
		case 3:
			if _, ok := session.TakeQueuedPromptBatch(ctx, "another_session"); ok {
				t.Error("batch escaped its session")
			}
			got, ok := session.TakeQueuedPromptBatch(ctx, sid)
			if !ok || !reflect.DeepEqual(got, expected) {
				t.Errorf("batch = %+v, want %+v", got, expected)
			}
			if _, ok := session.TakeQueuedPromptBatch(ctx, sid); ok {
				t.Error("batch was claimed twice")
			}
			if !session.BeginQueuedPromptRead(ctx, sid) {
				t.Error("batch could not be committed")
			}
		default:
			t.Errorf("unexpected run %d", runs)
		}
		return string(acp.StopReasonEndTurn), nil
	}
	m, sid = newQueueManagerWith(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
		done <- err
	}()
	awaitInterruptSignal(t, boundary)
	q, _, err := m.EnqueueTurnMessage(sid, "newer")
	if err != nil {
		t.Fatal(err)
	}
	expected = append(expected, q)
	if !m.TryInterruptQueuedTurn(sid) {
		t.Fatal("interrupt refused")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("continuation did not finish")
	}
	if runs != 3 {
		t.Fatalf("runs = %d", runs)
	}
	if got := m.SessionByID(sid).QueuedMessages(); len(got) != 0 {
		t.Fatalf("committed rows returned: %+v", got)
	}
}

func TestQueuedInterruptClaimWithoutCommitRetainsBatch(t *testing.T) {
	started := make(chan struct{})
	runs := 0
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		runs++
		if runs == 1 {
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		}
		if _, ok := session.TakeQueuedPromptBatch(ctx, st.GetID()); !ok {
			t.Error("missing batch")
		}
		return string(acp.StopReasonEndTurn), nil
	}
	m, sid := newQueueManagerWith(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
		done <- err
	}()
	awaitInterruptSignal(t, started)
	q, _, err := m.EnqueueTurnMessage(sid, "not read")
	if err != nil {
		t.Fatal(err)
	}
	if !m.TryInterruptQueuedTurn(sid) {
		t.Fatal("interrupt refused")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unacknowledged batch reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not settle")
	}
	if got := m.SessionByID(sid).QueuedMessages(); !reflect.DeepEqual(got, []session.QueuedMessage{q}) {
		t.Fatalf("unread batch = %+v", got)
	}
}

func TestQueuedInterruptSharesTheAdmissionFollowUpLimit(t *testing.T) {
	var m *session.Manager
	var sid string
	runs := 0
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		runs++
		_, batch := session.TakeQueuedPromptBatch(ctx, st.GetID())
		if !session.BeginQueuedPromptRead(ctx, st.GetID()) {
			return string(acp.StopReasonCancelled), nil
		}
		if runs > 2*session.MaxQueuedMessages {
			return string(acp.StopReasonEndTurn), nil
		}
		if _, _, err := m.EnqueueTurnMessageWithMode(sid, "next", session.QueueModeAfterTurn, nil); err != nil {
			return "", err
		}
		if !batch {
			if !m.TryInterruptQueuedTurn(sid) {
				t.Error("interrupt refused")
			}
			return string(acp.StopReasonCancelled), nil
		}
		return string(acp.StopReasonEndTurn), nil
	}
	m, sid = newQueueManagerWith(t, runner)
	_, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
	if err != nil {
		t.Fatal(err)
	}
	if runs > session.MaxQueuedMessages+1 {
		t.Fatalf("admitted turn ran %d times, limit is %d", runs, session.MaxQueuedMessages+1)
	}
}

func TestQueuedInterruptPreservesSupervisorContinuationBudget(t *testing.T) {
	var m *session.Manager
	var sid string
	var indices []int
	runs := 0
	interrupted := false
	runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		runs++
		session.TakeQueuedPromptBatch(ctx, sid)
		if !session.BeginQueuedPromptRead(ctx, sid) {
			return string(acp.StopReasonCancelled), nil
		}
		if marker := st.TakeTurnGoal(); marker != nil && marker.Kind == acp.GoalTurnContinue {
			indices = append(indices, marker.Index)
			if !interrupted {
				interrupted = true
				if _, _, err := m.EnqueueTurnMessage(sid, "correction"); err != nil {
					return "", err
				}
				if !m.TryInterruptQueuedTurn(sid) {
					t.Error("interrupt refused")
				}
				return string(acp.StopReasonCancelled), nil
			}
		}
		toolTurn(st, snd, fmt.Sprint(runs), "done")
		return string(acp.StopReasonEndTurn), nil
	}
	m, sid = newQueueManagerWith(t, runner)
	two := 2
	m.Cfg().Supervisor.Enable = true
	m.Cfg().Supervisor.MaxContinuations = &two
	m.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "not done", Remaining: []string{"finish"}}, nil
	})
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(indices, []int{1, 2}) {
		t.Fatalf("supervisor continuation indices = %v, want [1 2] without reset", indices)
	}
}

func TestQueuedInterruptHardStopBeforeBatchCommit(t *testing.T) {
	for _, action := range []string{"full_stop", "root_cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			started, claimed := make(chan struct{}), make(chan struct{})
			runs := 0
			runner := func(ctx context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
				runs++
				if runs == 1 {
					close(started)
					<-ctx.Done()
					return "", ctx.Err()
				}
				if _, ok := session.TakeQueuedPromptBatch(ctx, st.GetID()); !ok {
					t.Error("batch missing")
				}
				close(claimed)
				<-ctx.Done()
				if session.BeginQueuedPromptRead(ctx, st.GetID()) {
					t.Error("hard cancellation permitted the unread batch to append")
				}
				return string(acp.StopReasonCancelled), nil
			}
			dir := t.TempDir()
			cfg := testConfig()
			cfg.Paths.Home = dir
			cfg.Sessions.Dir = filepath.Join(dir, "sessions")
			m := session.NewManager(cfg, noopSender{}, runner, slog.Default(), dir, &session.FileStore{Root: cfg.Sessions.Dir})
			created, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
			if err != nil {
				t.Fatal(err)
			}
			sid := created.SessionID
			st := m.SessionByID(sid)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}})
				done <- err
			}()
			awaitInterruptSignal(t, started)
			q, _, err := m.EnqueueTurnMessage(sid, "unread")
			if err != nil {
				t.Fatal(err)
			}
			if !m.TryInterruptQueuedTurn(sid) {
				t.Fatal("interrupt refused")
			}
			awaitInterruptSignal(t, claimed)
			switch action {
			case "full_stop":
				m.HandleSessionCancel(acp.SessionCancelParams{SessionID: sid})
			case "root_cancel":
				st.Cancel()
			case "delete":
				if err := m.DeleteSessionTree(sid, nil); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("hard stop did not settle")
			}
			if runs != 2 {
				t.Fatalf("hard stop started another runner: %d", runs)
			}
			if action == "delete" {
				if m.SessionByID(sid) != nil {
					t.Fatal("deleted session came back")
				}
				if _, err := os.Stat(m.FileStore().SessionPath(sid)); !os.IsNotExist(err) {
					t.Fatalf("deleted bundle survived: %v", err)
				}
			} else if got := st.QueuedMessages(); !reflect.DeepEqual(got, []session.QueuedMessage{q}) {
				t.Fatalf("unread claimed batch = %+v", got)
			}
		})
	}
}
