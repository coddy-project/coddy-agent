package session

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// A queue that was never opened belongs to no turn, so it refuses rather than
// holding words for a conversation that may be hours away.
func TestEnqueueRefusesWithoutATurn(t *testing.T) {
	st := &State{ID: "sess_queue_closed"}
	if _, err := st.EnqueueMessage("a follow-up"); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("enqueue on a closed queue = %v, want %v", err, ErrNoActiveTurn)
	}
	if st.MessageQueueOpen() {
		t.Fatal("the queue reports itself open before any turn started")
	}
}

func TestEnqueueTrimsAndRefusesEmpty(t *testing.T) {
	st := &State{ID: "sess_queue_empty"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("   \n\t "); err == nil {
		t.Fatal("whitespace was accepted as a queued message")
	}
	msg, err := st.EnqueueMessage("  padded  ")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if msg.Text != "padded" {
		t.Fatalf("queued text = %q, want %q", msg.Text, "padded")
	}
	if msg.ID == "" || msg.CreatedAt == "" {
		t.Fatalf("queued message is missing its identity: %+v", msg)
	}
}

func TestQueueIsCapped(t *testing.T) {
	st := &State{ID: "sess_queue_cap"}
	st.OpenMessageQueue()
	for i := 0; i < MaxQueuedMessages; i++ {
		if _, err := st.EnqueueMessage(fmt.Sprintf("message %d", i)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := st.EnqueueMessage("one too many"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("enqueue past the cap = %v, want %v", err, ErrQueueFull)
	}
	if got := len(st.QueuedMessages()); got != MaxQueuedMessages {
		t.Fatalf("queue holds %d messages, want %d", got, MaxQueuedMessages)
	}
}

func TestCancelRemovesOnlyThatMessage(t *testing.T) {
	st := &State{ID: "sess_queue_cancel"}
	st.OpenMessageQueue()
	first, _ := st.EnqueueMessage("first")
	second, _ := st.EnqueueMessage("second")
	third, _ := st.EnqueueMessage("third")

	if !st.CancelQueuedMessage(second.ID) {
		t.Fatal("cancelling a queued message reported it missing")
	}
	if st.CancelQueuedMessage(second.ID) {
		t.Fatal("cancelling the same message twice reported it present")
	}
	if st.CancelQueuedMessage("q_nothing") {
		t.Fatal("cancelling an unknown id reported it present")
	}
	left := st.QueuedMessages()
	if len(left) != 2 || left[0].ID != first.ID || left[1].ID != third.ID {
		t.Fatalf("queue after the cancel = %+v, want %s then %s", left, first.ID, third.ID)
	}
}

// QueuedMessages hands back a copy: a caller that rewrites what it was given
// must not be rewriting the queue.
func TestQueuedMessagesIsACopy(t *testing.T) {
	st := &State{ID: "sess_queue_copy"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("original"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	snapshot := st.QueuedMessages()
	snapshot[0].Text = "rewritten"
	if got := st.QueuedMessages()[0].Text; got != "original" {
		t.Fatalf("the queue was mutated through its snapshot: %q", got)
	}
}

// The mid-turn drain leaves the queue open, so the next step can be corrected
// too; the boundary drain closes it only when it finds nothing.
func TestTakeQueuedMessagesKeepsTheQueueOpen(t *testing.T) {
	st := &State{ID: "sess_queue_drain"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("first"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := st.TakeQueuedMessages(); len(got) != 1 {
		t.Fatalf("drained %d messages, want 1", len(got))
	}
	if !st.MessageQueueOpen() {
		t.Fatal("the mid-turn drain closed the queue")
	}
	if _, err := st.EnqueueMessage("second"); err != nil {
		t.Fatalf("enqueue after the drain: %v", err)
	}
}

func TestTakeQueuedMessagesOrCloseClosesOnlyWhenEmpty(t *testing.T) {
	st := &State{ID: "sess_queue_boundary"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("late follow-up"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	got, more := st.TakeQueuedMessagesOrClose()
	if !more || len(got) != 1 {
		t.Fatalf("boundary drain = (%d, %v), want one message and more", len(got), more)
	}
	if !st.MessageQueueOpen() {
		t.Fatal("a boundary drain that found work closed the queue")
	}

	got, more = st.TakeQueuedMessagesOrClose()
	if more || len(got) != 0 {
		t.Fatalf("second boundary drain = (%d, %v), want nothing and done", len(got), more)
	}
	if st.MessageQueueOpen() {
		t.Fatal("the boundary drain found nothing and left the queue open")
	}
	if _, err := st.EnqueueMessage("too late"); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("enqueue after the boundary closed = %v, want %v", err, ErrNoActiveTurn)
	}
}

// Opening a turn's queue never inherits what an earlier turn did not read.
func TestOpenMessageQueueDropsWhatTheLastTurnLeft(t *testing.T) {
	st := &State{ID: "sess_queue_reopen"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("from the old turn"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	st.OpenMessageQueue()
	if got := st.QueuedMessages(); len(got) != 0 {
		t.Fatalf("the new turn inherited %d messages", len(got))
	}
}

func TestCloseMessageQueueReturnsLeftoversOnce(t *testing.T) {
	st := &State{ID: "sess_queue_close"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("never read"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if left := st.CloseMessageQueue(); len(left) != 1 {
		t.Fatalf("close returned %d leftovers, want 1", len(left))
	}
	if left := st.CloseMessageQueue(); len(left) != 0 {
		t.Fatalf("the second close returned %d leftovers, want none", len(left))
	}
}

func TestQueuedPromptBlocksJoinsInOrder(t *testing.T) {
	blocks := QueuedPromptBlocks([]QueuedMessage{{Text: "first"}, {Text: "second"}})
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want one prompt", len(blocks))
	}
	if want := "first\n\nsecond"; blocks[0].Text != want {
		t.Fatalf("prompt = %q, want %q", blocks[0].Text, want)
	}
}

// The queue is written by whoever is watching and read by the turn's goroutine,
// so the two must not need a lock of the caller's own.
func TestQueueIsSafeUnderConcurrentWritersAndDrains(t *testing.T) {
	st := &State{ID: "sess_queue_race"}
	st.OpenMessageQueue()

	var wg sync.WaitGroup
	var mu sync.Mutex
	read := map[string]bool{}

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			// The cap is small on purpose, so a full queue is an ordinary
			// answer here rather than a failure.
			_, _ = st.EnqueueMessage(fmt.Sprintf("m%d", i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for _, m := range st.TakeQueuedMessages() {
				mu.Lock()
				if read[m.ID] {
					mu.Unlock()
					t.Errorf("message %s was read twice", m.ID)
					return
				}
				read[m.ID] = true
				mu.Unlock()
			}
		}
	}()
	wg.Wait()

	for _, m := range st.CloseMessageQueue() {
		if read[m.ID] {
			t.Fatalf("message %s was both read and left in the queue", m.ID)
		}
	}
}

func TestManagerQueueRefusesUnknownSession(t *testing.T) {
	m := &Manager{}
	if _, _, err := m.EnqueueTurnMessage("sess_missing", "text"); err == nil ||
		!strings.Contains(err.Error(), "session not found") {
		t.Fatalf("enqueue on a missing session = %v, want a not-found error", err)
	}
	if _, _, err := m.EnqueueTurnMessage("  ", "text"); err == nil {
		t.Fatal("enqueue accepted an empty session id")
	}
}
