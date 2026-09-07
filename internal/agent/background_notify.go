package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// maxWakesPerSession bounds how many autonomous turns one session may be woken
// into during the life of this process.
//
// A woken turn can start another notifying task, which wakes another turn: that
// is a legitimate pattern for unattended work, and also a way to burn a night's
// worth of tokens on a loop nobody is watching. The cap is a backstop, not a
// policy - reaching it does not lose the notice, it only stops starting turns
// for it.
const maxWakesPerSession = 50

// wakeSettleDelay lets a burst of tasks finishing together become one turn
// instead of several. Waking once with three outcomes is both cheaper and
// easier for the model to act on than three turns that each see one.
const wakeSettleDelay = 750 * time.Millisecond

// RunTurnFunc runs an autonomous agent turn for a session. The implementation
// is expected to take the composer turn lock, so a wake-up waits for a turn
// already in flight rather than racing it.
type RunTurnFunc func(ctx context.Context, sessionID, instruction string) error

// BackgroundWaker turns finished background tasks into agent turns, so a
// session keeps moving while nobody is watching it.
//
// Only tasks the model explicitly marked with notify_on_finish are eligible:
// the model decides what is worth a turn, so a batch of quick commands cannot
// each start one behind the operator's back.
type BackgroundWaker struct {
	log  *slog.Logger
	run  RunTurnFunc
	pool *bgtask.Pool

	mu       sync.Mutex
	pending  map[string][]bgtask.Snapshot
	draining map[string]bool
	wakes    map[string]int
}

// NewBackgroundWaker returns a waker that runs turns through run.
func NewBackgroundWaker(log *slog.Logger, run RunTurnFunc) *BackgroundWaker {
	if log == nil {
		log = slog.Default()
	}
	return &BackgroundWaker{
		log:      log,
		run:      run,
		pending:  map[string][]bgtask.Snapshot{},
		draining: map[string]bool{},
		wakes:    map[string]int{},
	}
}

// BackgroundWakerKey identifies the waker's pool subscription, so rebuilding
// the component that owns it replaces the watcher instead of stacking another.
const BackgroundWakerKey = "agent.background_waker"

// Attach subscribes the waker to a pool, replacing any previous waker.
func (w *BackgroundWaker) Attach(pool *bgtask.Pool) {
	if w == nil || pool == nil || w.run == nil {
		return
	}
	w.pool = pool
	pool.SubscribeKeyed(BackgroundWakerKey, w.OnSnapshot)
}

// OnSnapshot is the pool watcher. It must not block: the pool calls it from the
// goroutine that just finished supervising a task.
func (w *BackgroundWaker) OnSnapshot(snap bgtask.Snapshot) {
	if !snap.NotifyOnFinish || !snap.Status.Finished() {
		return
	}
	sessionID := strings.TrimSpace(snap.SessionID)
	if sessionID == "" {
		return
	}

	w.mu.Lock()
	w.pending[sessionID] = append(w.pending[sessionID], snap)
	if w.draining[sessionID] {
		w.mu.Unlock()
		return
	}
	w.draining[sessionID] = true
	w.mu.Unlock()

	go w.drain(sessionID)
}

// drain waits briefly for stragglers, then runs one turn per batch until the
// queue for this session is empty.
func (w *BackgroundWaker) drain(sessionID string) {
	defer func() {
		w.mu.Lock()
		delete(w.draining, sessionID)
		w.mu.Unlock()
	}()

	for {
		time.Sleep(wakeSettleDelay)

		w.mu.Lock()
		batch := w.pending[sessionID]
		delete(w.pending, sessionID)
		if len(batch) == 0 {
			w.mu.Unlock()
			return
		}
		if w.wakes[sessionID] >= maxWakesPerSession {
			w.mu.Unlock()
			w.log.Warn("background_wake_capped",
				"session_id", sessionID,
				"limit", maxWakesPerSession,
				"dropped", len(batch))
			return
		}
		w.wakes[sessionID]++
		w.mu.Unlock()

		// Shutdown stops tasks, and stopping them is itself a terminal status.
		// Waking the model into a turn nobody will read, while the process is
		// going away, is pure waste.
		if w.pool != nil && w.pool.Draining() {
			w.log.Info("background_wake_skipped_draining", "session_id", sessionID, "tasks", len(batch))
			return
		}

		instruction := WakeInstruction(batch)
		w.log.Info("background_wake_start", "session_id", sessionID, "tasks", len(batch))
		if err := w.run(context.Background(), sessionID, instruction); err != nil {
			w.log.Warn("background_wake_failed", "session_id", sessionID, "error", err)
			return
		}
		w.log.Info("background_wake_finish", "session_id", sessionID)
	}
}

// WakeInstruction renders the user-role message a woken turn starts from. It
// states the outcome plainly, including failure, so the model does not have to
// guess whether the work succeeded, and points at the tool that has the detail
// rather than pasting a wall of output into the prompt.
func WakeInstruction(batch []bgtask.Snapshot) string {
	var b strings.Builder
	if len(batch) == 1 {
		b.WriteString("A background task you asked to be notified about has finished.\n\n")
	} else {
		fmt.Fprintf(&b, "%d background tasks you asked to be notified about have finished.\n\n", len(batch))
	}

	for _, t := range batch {
		fmt.Fprintf(&b, "- %s [%s] %s", t.ID, t.Status, t.Label)
		if t.ExitCode != nil {
			fmt.Fprintf(&b, ", exit %d", *t.ExitCode)
		}
		elapsed := int(t.Elapsed(time.Now()).Round(time.Second) / time.Second)
		fmt.Fprintf(&b, ", ran %ds", elapsed)
		if t.Error != "" {
			fmt.Fprintf(&b, ", error: %s", t.Error)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nRead the output with background_output when you need it. ")
	b.WriteString("Continue the work this task was part of, and report the outcome honestly: ")
	b.WriteString("a task that failed, timed out, or was stopped did not succeed.")
	return b.String()
}
