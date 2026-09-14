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
	accepted := map[string]bool{}

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			// The cap is small on purpose, so a full queue is an ordinary
			// answer here rather than a failure. Only what the queue accepted
			// is something it then owes the turn.
			msg, err := st.EnqueueMessage(fmt.Sprintf("m%d", i))
			if err != nil {
				continue
			}
			mu.Lock()
			accepted[msg.ID] = true
			mu.Unlock()
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

	left := map[string]bool{}
	for _, m := range st.CloseMessageQueue() {
		if read[m.ID] {
			t.Fatalf("message %s was both read and left in the queue", m.ID)
		}
		left[m.ID] = true
	}
	// Accepting a message is a promise: it is either read by the turn or still
	// waiting when the turn ends. Anything in neither set was dropped in the
	// middle, which is the failure this test exists to catch.
	mu.Lock()
	defer mu.Unlock()
	for id := range accepted {
		if !read[id] && !left[id] {
			t.Fatalf("message %s was accepted and then lost: neither read nor left in the queue", id)
		}
	}
	if len(accepted) == 0 {
		t.Fatal("the queue accepted nothing; the test proved nothing")
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

// Every change announces itself once through the notifier the manager installs,
// and the snapshot it reads back names the version of exactly that content.
func TestQueueNotifierFiresOnEveryChangeWithAMatchingSnapshot(t *testing.T) {
	st := &State{ID: "sess_queue_notify"}
	type seen struct {
		texts   []string
		version uint64
	}
	var mu sync.Mutex
	var got []seen
	st.SetQueueNotifier(func() {
		msgs, v := st.QueueSnapshot()
		texts := make([]string, 0, len(msgs))
		for _, m := range msgs {
			texts = append(texts, m.Text)
		}
		mu.Lock()
		got = append(got, seen{texts: texts, version: v})
		mu.Unlock()
	})

	st.OpenMessageQueue()
	first, err := st.EnqueueMessage("first")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := st.EnqueueMessage("second"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !st.CancelQueuedMessage(first.ID) {
		t.Fatal("cancel reported the message missing")
	}
	st.TakeQueuedMessages()
	st.CloseMessageQueue()

	mu.Lock()
	defer mu.Unlock()
	wantTexts := [][]string{
		{},                  // open
		{"first"},           // enqueue
		{"first", "second"}, // enqueue
		{"second"},          // cancel
		{},                  // drain
		{},                  // close
	}
	if len(got) != len(wantTexts) {
		t.Fatalf("notifier fired %d times, want %d: %+v", len(got), len(wantTexts), got)
	}
	for i, want := range wantTexts {
		if len(got[i].texts) != len(want) {
			t.Fatalf("notification %d carried %v, want %v", i, got[i].texts, want)
		}
		for j := range want {
			if got[i].texts[j] != want[j] {
				t.Fatalf("notification %d carried %v, want %v", i, got[i].texts, want)
			}
		}
		if i > 0 && got[i].version <= got[i-1].version {
			t.Fatalf("version did not advance at notification %d: %d then %d",
				i, got[i-1].version, got[i].version)
		}
	}
}

// A cancel that takes the last message and a drain that takes it first must not
// be able to hand a watcher a stale list under a fresh version: the snapshot is
// read under the same lock the change was made under.
func TestQueueSnapshotPairsContentWithItsOwnVersion(t *testing.T) {
	st := &State{ID: "sess_queue_snapshot"}
	st.OpenMessageQueue()
	if _, err := st.EnqueueMessage("only"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	msgs, v1 := st.QueueSnapshot()
	if len(msgs) != 1 {
		t.Fatalf("snapshot holds %d messages, want 1", len(msgs))
	}
	st.TakeQueuedMessages()
	empty, v2 := st.QueueSnapshot()
	if len(empty) != 0 {
		t.Fatalf("snapshot after the drain holds %d messages", len(empty))
	}
	if v2 <= v1 {
		t.Fatalf("version did not advance across the drain: %d then %d", v1, v2)
	}
}

// The version is process-wide, so a session whose live state is rebuilt does
// not publish numbers a client has already applied and would now ignore.
func TestQueueVersionDoesNotRestartWithTheState(t *testing.T) {
	first := &State{ID: "sess_queue_epoch"}
	first.OpenMessageQueue()
	if _, err := first.EnqueueMessage("before"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	_, highWater := first.QueueSnapshot()

	rebuilt := &State{ID: "sess_queue_epoch"}
	rebuilt.OpenMessageQueue()
	if _, v := rebuilt.QueueSnapshot(); v <= highWater {
		t.Fatalf("the rebuilt session published version %d, at or below the %d a client already applied", v, highWater)
	}
}
