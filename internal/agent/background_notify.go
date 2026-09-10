package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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

// How long a wake waits for a session whose turn is still in flight. See
// startTurn for why that wait exists at all.
const (
	// wakeBusyRetryFirst is how long the first refusal waits.
	wakeBusyRetryFirst = 1 * time.Second
	// wakeBusyRetryMax caps the backoff, so a long turn is polled cheaply
	// without leaving a finished task unreported for minutes after it ends.
	wakeBusyRetryMax = 15 * time.Second
	// wakeBusyGiveUpAfter bounds the whole wait. A turn still running after
	// this is not going to end on its own, and one retry goroutine per session
	// must not outlive the process.
	wakeBusyGiveUpAfter = 30 * time.Minute
)

// RunTurnFunc runs an autonomous agent turn for a session.
//
// It takes the composer turn lock, which refuses rather than queues: a session
// with a turn already in flight answers session.ErrSessionTurnBusy, and the
// waker treats that as "try again shortly", not as a lost wake. Every other
// error is the turn's own and ends the attempt.
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

	// busyRetryFirst, busyRetryMax and busyGiveUpAfter shape the wait for a
	// session whose turn is still in flight. They are fields rather than
	// constants so tests can compress the schedule.
	busyRetryFirst  time.Duration
	busyRetryMax    time.Duration
	busyGiveUpAfter time.Duration

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
		log:             log,
		run:             run,
		busyRetryFirst:  wakeBusyRetryFirst,
		busyRetryMax:    wakeBusyRetryMax,
		busyGiveUpAfter: wakeBusyGiveUpAfter,
		pending:         map[string][]bgtask.Snapshot{},
		draining:        map[string]bool{},
		wakes:           map[string]int{},
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
// queue for this session is empty. A batch is only ever given up on by
// startTurn, never by the loop: a task that finished has an outcome the model
// was promised.
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

		if !w.startTurn(sessionID, batch) {
			return
		}
	}
}

// startTurn runs one wake turn, waiting out a session that already has a turn
// in flight, and reports whether the drain loop should keep going.
//
// A busy session is the ordinary case: the model starts a task and keeps
// talking, so anything that fails in its first seconds finishes inside the very
// turn that launched it. The composer turn lock refuses rather than queues, and
// treating that refusal as a lost wake is what left a failed task unreported -
// the outcome the model was promised never arrived, and nothing said so.
//
// Anything else is the turn's own failure, not contention, and retrying it
// would spin: the batch is dropped and the reason logged, as before.
func (w *BackgroundWaker) startTurn(sessionID string, batch []bgtask.Snapshot) bool {
	deadline := time.Now().Add(w.busyGiveUpAfter)
	delay := w.busyRetryFirst

	for attempt := 1; ; attempt++ {
		// Shutdown stops tasks, and stopping them is itself a terminal status.
		// Waking the model into a turn nobody will read, while the process is
		// going away, is pure waste.
		if w.pool != nil && w.pool.Draining() {
			w.log.Info("background_wake_skipped_draining", "session_id", sessionID, "tasks", len(batch))
			w.refundWake(sessionID)
			return false
		}

		// Whatever landed while the last attempt was refused belongs in this
		// turn: a wait of minutes must not leave the model a stale batch and a
		// second turn queued behind it.
		batch = w.absorbPending(sessionID, batch)

		w.log.Info("background_wake_start", "session_id", sessionID, "tasks", len(batch), "attempt", attempt)
		err := w.run(context.Background(), sessionID, WakeInstruction(batch))
		switch {
		case err == nil:
			w.log.Info("background_wake_finish", "session_id", sessionID)
			return true
		case !errors.Is(err, session.ErrSessionTurnBusy):
			w.log.Warn("background_wake_failed", "session_id", sessionID, "error", err)
			w.refundWake(sessionID)
			return false
		case time.Now().After(deadline):
			// A turn still running after the whole window is not going to end
			// on its own, and one retry goroutine per session must not outlive
			// the process.
			w.log.Warn("background_wake_abandoned",
				"session_id", sessionID,
				"tasks", len(batch),
				"waited", w.busyGiveUpAfter,
				"attempts", attempt)
			w.refundWake(sessionID)
			return false
		}

		w.log.Debug("background_wake_busy", "session_id", sessionID, "attempt", attempt, "retry_in", delay)
		time.Sleep(delay)
		delay = min(delay*2, w.busyRetryMax)
	}
}

// absorbPending folds everything queued for the session into the batch in
// flight, so a wait for a busy session costs one turn rather than one per
// task that finished during it.
func (w *BackgroundWaker) absorbPending(sessionID string, batch []bgtask.Snapshot) []bgtask.Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	queued := w.pending[sessionID]
	if len(queued) == 0 {
		return batch
	}
	delete(w.pending, sessionID)
	return append(batch, queued...)
}

// refundWake gives back the budget reserved for a turn that never ran. The cap
// counts turns the model was actually woken into, so an attempt refused by a
// busy session or by shutdown must not spend one.
func (w *BackgroundWaker) refundWake(sessionID string) {
	w.mu.Lock()
	if w.wakes[sessionID] > 0 {
		w.wakes[sessionID]--
	}
	w.mu.Unlock()
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
