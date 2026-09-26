package session_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// queueRunner is a stub agent that does not read the queue itself, so the
// manager's turn boundary is the only thing that can pick a late follow-up up.
type queueRunner struct {
	mu sync.Mutex
	// prompts records the text each run was started with, in order.
	prompts []string
	// onRun runs inside the turn, standing in for the operator typing while
	// the answer is being returned. It is called with the run index.
	onRun func(run int)
	// stop is what each run reports; the default is an ordinary answer.
	stop string
}

func (r *queueRunner) run(_ context.Context, _ *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
	r.mu.Lock()
	idx := len(r.prompts)
	var text strings.Builder
	for _, b := range prompt {
		text.WriteString(b.Text)
	}
	r.prompts = append(r.prompts, text.String())
	hook := r.onRun
	stop := r.stop
	r.mu.Unlock()
	if hook != nil {
		hook(idx)
	}
	if stop == "" {
		stop = string(acp.StopReasonEndTurn)
	}
	return stop, nil
}

func (r *queueRunner) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

func newQueueManager(t *testing.T, r *queueRunner) (*session.Manager, string) {
	t.Helper()
	return newQueueManagerWith(t, r.run)
}

func newQueueManagerWith(t *testing.T, run session.AgentRunner) (*session.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := session.NewManager(testConfig(), noopSender{}, run, slog.Default(), dir, nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	return m, res.SessionID
}

// A message written in the final moments of a turn - after the loop's last read
// and before the turn released - is answered by that same turn rather than
// waiting for the next prompt.
func TestTurnBoundaryAnswersAFollowUpTheLoopNeverSaw(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		if _, _, err := mgr.EnqueueTurnMessage(sid, "one more thing"); err != nil {
			t.Errorf("enqueue during the turn: %v", err)
		}
	}
	mgr, sid = newQueueManager(t, r)

	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "start the work"}},
	}); err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}

	got := r.seen()
	if len(got) != 2 {
		t.Fatalf("the runner ran %d times, want 2: %v", len(got), got)
	}
	if got[1] != "one more thing" {
		t.Fatalf("the follow-up run was started with %q", got[1])
	}
	left, err := mgr.QueuedTurnMessages(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("the queue still holds %d messages after the turn", len(left))
	}
}

func TestAfterTurnMessagesRunOneAtATime(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		for _, text := range []string{"first deferred", "second deferred"} {
			if _, _, err := mgr.EnqueueTurnMessageWithMode(sid, text, session.QueueModeAfterTurn, nil); err != nil {
				t.Error(err)
			}
		}
	}
	mgr, sid = newQueueManager(t, r)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.seen(); len(got) != 3 || got[0] != "start" || got[1] != "first deferred" || got[2] != "second deferred" {
		t.Fatalf("runs = %v", got)
	}
}

func TestFullAfterTurnQueueRunsEveryMessage(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		for i := 0; i < session.MaxQueuedMessages; i++ {
			if _, _, err := mgr.EnqueueTurnMessageWithMode(sid, fmt.Sprintf("deferred %d", i), session.QueueModeAfterTurn, nil); err != nil {
				t.Error(err)
			}
		}
	}
	mgr, sid = newQueueManager(t, r)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.seen(); len(got) != session.MaxQueuedMessages+1 {
		t.Fatalf("ran %d prompts, want %d", len(got), session.MaxQueuedMessages+1)
	}
}

func TestStopKeepsAfterTurnWithoutStartingIt(t *testing.T) {
	r := &queueRunner{stop: string(acp.StopReasonCancelled)}
	var mgr *session.Manager
	var sid string
	r.onRun = func(run int) {
		if _, _, err := mgr.EnqueueTurnMessageWithMode(sid, "after stop", session.QueueModeAfterTurn, nil); err != nil {
			t.Error(err)
		}
	}
	mgr, sid = newQueueManager(t, r)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.seen(); len(got) != 1 {
		t.Fatalf("runs = %v", got)
	}
	rows, err := mgr.QueuedTurnMessages(sid)
	if err != nil || len(rows) != 1 || rows[0].Text != "after stop" {
		t.Fatalf("queued = %+v, %v", rows, err)
	}
}

// A settings command with an image and no text applies at once, and the image
// is still queued: the command is the operator's, the image is a message.
func TestSettingsCommandWithAnImageQueuesTheImage(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string
	var msg session.QueuedMessage
	var queued bool
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		parts := []acp.ImagePartRef{{Name: "shot.png", DataURL: "data:image/png;base64,YQ=="}}
		var err error
		msg, queued, _, err = mgr.EnqueueFollowUpWithMode(context.Background(), sid, "/ask", "web", session.QueueModeAfterTurn, parts)
		if err != nil {
			t.Errorf("enqueue: %v", err)
		}
	}
	mgr, sid = newQueueManager(t, r)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if !queued || len(msg.ImageParts) != 1 || msg.ImageParts[0].Name != "shot.png" || strings.TrimSpace(msg.Text) != "" {
		t.Fatalf("queued=%v message=%+v; want the image queued without the command", queued, msg)
	}
	if got := mgr.SessionByID(sid).GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("session mode = %q, want the command applied", got)
	}
	if got := r.seen(); len(got) != 2 {
		t.Fatalf("runs = %q; want the image to start a run of its own after the answer", got)
	}
}

// deferredStopRunner answers the first prompt after queueing one after_turn
// message, then stops the turn from inside the run that message starts -
// before or after that run puts its prompt into the conversation.
func deferredStopRunner(t *testing.T, mgr **session.Manager, sid *string, addFirst bool) (session.AgentRunner, func() []string) {
	var mu sync.Mutex
	var prompts []string
	run := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		text := ""
		if len(prompt) > 0 {
			text = prompt[0].Text
		}
		mu.Lock()
		idx := len(prompts)
		prompts = append(prompts, text)
		mu.Unlock()
		if idx == 0 {
			if _, _, err := (*mgr).EnqueueTurnMessageWithMode(*sid, "deferred", session.QueueModeAfterTurn, nil); err != nil {
				t.Error(err)
			}
			return string(acp.StopReasonEndTurn), nil
		}
		if addFirst {
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		}
		(*mgr).HandleSessionCancel(acp.SessionCancelParams{SessionID: *sid})
		return string(acp.StopReasonCancelled), nil
	}
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), prompts...)
	}
	return run, seen
}

// A Stop that lands after the boundary took a deferred message, but before its
// run put the prompt into the conversation, must not lose it: nothing read it,
// and a Stop keeps after_turn messages for later.
func TestStopBeforeADeferredPromptIsReadKeepsItQueued(t *testing.T) {
	var mgr *session.Manager
	var sid string
	run, seen := deferredStopRunner(t, &mgr, &sid, false)
	mgr, sid = newQueueManagerWith(t, run)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if got := seen(); len(got) != 2 || got[1] != "deferred" {
		t.Fatalf("runs = %v, want the prompt and the deferred message", got)
	}
	rows, err := mgr.QueuedTurnMessages(sid)
	if err != nil || len(rows) != 1 || rows[0].Text != "deferred" || rows[0].Mode != session.QueueModeAfterTurn {
		t.Fatalf("queued after the Stop = %+v, %v; want the deferred message back", rows, err)
	}
}

// Once the run has put the deferred prompt into the conversation, a Stop ends
// that run like any other: the message is read, so it is not queued again.
func TestStopAfterADeferredPromptIsReadDoesNotQueueItAgain(t *testing.T) {
	var mgr *session.Manager
	var sid string
	run, seen := deferredStopRunner(t, &mgr, &sid, true)
	mgr, sid = newQueueManagerWith(t, run)
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "start"}}}); err != nil {
		t.Fatal(err)
	}
	if got := seen(); len(got) != 2 {
		t.Fatalf("runs = %v", got)
	}
	if rows, err := mgr.QueuedTurnMessages(sid); err != nil || len(rows) != 0 {
		t.Fatalf("queued after the Stop = %+v, %v; the read message came back", rows, err)
	}
}

// A cancelled turn is a Stop, and a Stop drops unread steer messages instead of
// answering it: the operator asked the work to end, not to continue.
func TestACancelledTurnDropsTheQueueInsteadOfAnsweringIt(t *testing.T) {
	r := &queueRunner{stop: string(acp.StopReasonCancelled)}
	var mgr *session.Manager
	var sid string
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		if _, _, err := mgr.EnqueueTurnMessage(sid, "never mind, do this instead"); err != nil {
			t.Errorf("enqueue during the turn: %v", err)
		}
	}
	mgr, sid = newQueueManager(t, r)

	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "start the work"}},
	}); err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}

	if got := r.seen(); len(got) != 1 {
		t.Fatalf("a cancelled turn ran the runner %d times, want 1: %v", len(got), got)
	}
	left, err := mgr.QueuedTurnMessages(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("the cancelled turn left %d queued messages behind", len(left))
	}
}

// One admitted turn cannot be kept alive forever by queueing into every
// boundary: past the cap the queue is closed and the session is released.
func TestTurnBoundaryStopsAfterTheFollowUpCap(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string
	r.onRun = func(int) {
		// Always another one waiting, the way a script pointed at the endpoint
		// would behave.
		_, _, _ = mgr.EnqueueTurnMessage(sid, "and one more")
	}
	mgr, sid = newQueueManager(t, r)

	done := make(chan error, 1)
	go func() {
		_, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sid,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "start the work"}},
		})
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}

	got := r.seen()
	if len(got) < 2 {
		t.Fatalf("the boundary never continued the turn: %v", got)
	}
	if len(got) > session.MaxQueuedMessages+1 {
		t.Fatalf("the boundary ran the runner %d times: the cap does not hold", len(got))
	}
	// The session must be usable again: a turn that never released would be
	// worse than a dropped follow-up.
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "next prompt"}},
	}); err != nil {
		t.Fatalf("the session never released its turn: %v", err)
	}
}

// Everything a client is told about the queue reaches every observer, not only
// the sender of the turn that happens to be running.
func TestQueueObserversSeeEveryChangeOfAnyClient(t *testing.T) {
	r := &queueRunner{}
	var mgr *session.Manager
	var sid string

	var mu sync.Mutex
	var seen []acp.MessageQueueUpdate
	r.onRun = func(run int) {
		if run != 0 {
			return
		}
		if _, _, err := mgr.EnqueueTurnMessage(sid, "watch this"); err != nil {
			t.Errorf("enqueue: %v", err)
		}
	}
	mgr, sid = newQueueManager(t, r)
	remove := mgr.AddMessageQueueObserver(func(u acp.MessageQueueUpdate) {
		mu.Lock()
		seen = append(seen, u)
		mu.Unlock()
	})
	defer remove()

	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sid,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "start the work"}},
	}); err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("no observer ever heard about the queue")
	}
	sawMessage, sawDrain := false, false
	var last uint64
	for _, u := range seen {
		if u.SessionID != sid {
			t.Fatalf("an update named session %q, want %q", u.SessionID, sid)
		}
		if u.Version <= last {
			t.Fatalf("versions did not advance: %d after %d", u.Version, last)
		}
		last = u.Version
		if len(u.Messages) == 1 && u.Messages[0].Text == "watch this" {
			sawMessage = true
		}
		if sawMessage && len(u.Messages) == 0 {
			sawDrain = true
		}
	}
	if !sawMessage {
		t.Fatalf("observers never saw the queued message: %+v", seen)
	}
	if !sawDrain {
		t.Fatalf("observers never saw the queue emptied after it was read: %+v", seen)
	}
}
