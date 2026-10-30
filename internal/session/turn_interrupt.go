package session

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

var errQueuedTurnInterrupt = fmt.Errorf("continue with queued messages: %w", context.Canceled)

type queuedPromptKey struct{}
type interruptRunKey struct{}
type queuedRunBudgetKey struct{}

// Only the admitted turn goroutine spends this shared boundary/interrupt budget.
type queuedRunBudget struct{ runs int }

// queuedPromptReceipt owns the original rows until their append starts. A
// boundary prompt may still use the ordinary single-prompt path; an interrupt
// transfers its unread originals to a batch instead of replaying joined text.
type queuedPromptReceipt struct {
	state       *State
	rows        []QueuedMessage
	batch       bool
	claimed     atomic.Bool
	read        atomic.Bool
	transferred atomic.Bool
}

// queuedTurnInterrupt is guarded by State.queueMu. The admitted turn's cancel
// remains on State; only the supervised run below it is interrupted here.
type queuedTurnInterrupt struct {
	state   *State
	root    context.Context
	cancel  context.CancelCauseFunc
	input   *queuedPromptReceipt
	pending *queuedPromptReceipt
	// supervisor belongs to the run goroutine, not the queue notifier.
	supervisor *goalRun
}

func withQueuedPromptReceipt(ctx context.Context, st *State, rows []QueuedMessage, batch bool) (context.Context, *queuedPromptReceipt) {
	r := &queuedPromptReceipt{state: st, rows: rows, batch: batch}
	return context.WithValue(ctx, queuedPromptKey{}, r), r
}

func queuedPromptOf(ctx context.Context, sessionID string) *queuedPromptReceipt {
	r, _ := ctx.Value(queuedPromptKey{}).(*queuedPromptReceipt)
	if r == nil || r.state.GetID() != sessionID {
		return nil
	}
	return r
}

// hasQueuedPromptBatch identifies the first dispatch of a reserved batch. It
// does not claim it, so the supervisor can avoid interpreting joined commands.
func hasQueuedPromptBatch(ctx context.Context, sessionID string) bool {
	r := queuedPromptOf(ctx, sessionID)
	return r != nil && r.batch && !r.claimed.Load() && !r.read.Load()
}

// TakeQueuedPromptBatch claims a run's queued input once, only for its session.
// The runner must call BeginQueuedPromptRead immediately before appending all
// rows, without cancellation checks between those appends. A claim alone does
// not consume anything: an unread batch is retained when the turn unwinds.
func TakeQueuedPromptBatch(ctx context.Context, sessionID string) ([]QueuedMessage, bool) {
	r := queuedPromptOf(ctx, sessionID)
	if r == nil || !r.batch || !r.claimed.CompareAndSwap(false, true) {
		return nil, false
	}
	return append([]QueuedMessage(nil), r.rows...), true
}

// BeginQueuedPromptRead linearizes a queued input's append against Stop. Once
// true, the runner commits the whole bounded batch even if cancellation arrives
// during persistence; deletion waits for that runner. False forbids the append.
// Claiming the batch itself remains single-use.
func BeginQueuedPromptRead(ctx context.Context, sessionID string) bool {
	r := queuedPromptOf(ctx, sessionID)
	if r == nil {
		// An admitted prompt is not a queued receipt: it has no queue to go
		// back to, so it reaches the transcript whatever cancelled its run,
		// and a queued batch follows it.
		return true
	}
	st := r.state
	st.queueMu.Lock()
	defer st.queueMu.Unlock()
	if ctx.Err() != nil || r.transferred.Load() {
		return false
	}
	r.read.Store(true)
	if c := st.queueInterrupt; c != nil && c.pending == r {
		c.pending = nil
	}
	return true
}

// IsQueuedTurnInterrupt distinguishes the deliberate child-run interruption
// from a full Stop, a shutdown, a watchdog or a provider timeout.
func IsQueuedTurnInterrupt(ctx context.Context, sessionID string) bool {
	c, _ := ctx.Value(interruptRunKey{}).(*queuedTurnInterrupt)
	return c != nil && c.state.GetID() == sessionID && errors.Is(context.Cause(ctx), errQueuedTurnInterrupt)
}

// TryInterruptQueuedTurn reserves every waiting message in FIFO order and
// interrupts only the current supervised run. False requires the caller to
// use ordinary full cancellation; in particular, a second Stop cannot queue
// another restart while the first batch is still unread. No disk cancel marker
// is written on this path.
func (m *Manager) TryInterruptQueuedTurn(sessionID string) bool {
	st := m.getSession(sessionID)
	if st == nil {
		return false
	}
	st.queueMu.Lock()
	c := st.queueInterrupt
	if c == nil || c.cancel == nil || c.pending != nil || c.root.Err() != nil || !st.queueOpen || st.IsUserCancelledTurn() {
		st.queueMu.Unlock()
		return false
	}
	var rows []QueuedMessage
	if r := c.input; r != nil && !r.read.Load() && !r.transferred.Load() {
		rows = append(rows, r.rows...)
	}
	rows = append(rows, st.queue...)
	if len(rows) == 0 {
		st.queueMu.Unlock()
		return false
	}
	if r := c.input; r != nil && !r.read.Load() {
		r.transferred.Store(true)
	}
	c.pending = &queuedPromptReceipt{state: st, rows: rows, batch: true}
	st.queue = nil
	st.bumpQueueLocked()
	c.cancel(errQueuedTurnInterrupt)
	st.queueMu.Unlock()
	st.notifyQueue()
	return true
}

// runInterruptibleTurn keeps one admission and one writer across the child
// runs. Unsupported admissions retain their existing cancellation semantics.
func (m *Manager) runInterruptibleTurn(ctx context.Context, st *State, prompt []acp.ContentBlock, sender acp.UpdateSender, opts *PromptRunOpts) (string, error) {
	if st.Subagent() != nil || isAgentCommandPrompt(prompt) || ParseGoalCommand(contentBlocksToPlainText(prompt)).Kind != GoalCommandNone ||
		(opts != nil && (opts.subagentTurn || opts.BackgroundWake != nil || opts.goalKickoff != nil)) {
		return m.runSupervisedTurn(ctx, st, prompt, sender, opts)
	}
	budget, _ := ctx.Value(queuedRunBudgetKey{}).(*queuedRunBudget)
	if budget == nil {
		budget = &queuedRunBudget{}
	}
	c := &queuedTurnInterrupt{state: st, root: ctx, input: queuedPromptOf(ctx, st.GetID())}
	st.queueMu.Lock()
	st.queueInterrupt = c
	st.queueMu.Unlock()
	defer st.finishQueuedInterrupt(c)
	runBase := context.WithValue(ctx, interruptRunKey{}, c)
	firstDispatch := true
	for {
		runCtx, cancel := context.WithCancelCause(runBase)
		st.queueMu.Lock()
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			st.queueMu.Unlock()
			cancel(nil)
			if firstDispatch {
				// Legacy cancellation still enters the original runner for
				// its cleanup, including a Stop during MCP preparation.
				return m.runSupervisedTurn(ctx, st, prompt, sender, opts)
			}
			return string(acp.StopReasonCancelled), ctx.Err()
		}
		firstDispatch = false
		c.cancel = cancel
		st.queueMu.Unlock()
		stop, err := m.runSupervisedTurn(runCtx, st, prompt, sender, opts)
		// Completion and interrupt acceptance share the queue lock: never
		// accept a batch after classifying this run's cancellation cause.
		st.queueMu.Lock()
		c.cancel = nil
		interrupted := IsQueuedTurnInterrupt(runCtx, st.GetID())
		cancel(nil)
		pending := c.pending
		if pending == nil {
			unread := c.input != nil && c.input.batch && !c.input.read.Load()
			st.queueMu.Unlock()
			if unread && err == nil {
				return stop, fmt.Errorf("queued prompt was not read by the runner")
			}
			return stop, err
		}
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			st.queueMu.Unlock()
			return string(acp.StopReasonCancelled), ctx.Err()
		}
		if !interrupted || (err != nil && !errors.Is(err, context.Canceled)) {
			st.queueMu.Unlock()
			if pending == c.input && err == nil {
				return stop, fmt.Errorf("queued prompt was not read by the runner")
			}
			return stop, err
		}
		if budget.runs >= maxQueuedFollowUpRuns {
			st.queueMu.Unlock()
			st.SetTurnStopNotice("Stopped after the queued follow-up run limit; unread messages remain queued.")
			return string(acp.StopReasonMaxTurns), nil
		}
		budget.runs++
		c.input = pending
		runBase = context.WithValue(context.WithValue(ctx, interruptRunKey{}, c), queuedPromptKey{}, pending)
		prompt = QueuedPromptBlocks(pending.rows)
		st.queueMu.Unlock()
		// A boundary input may have prepared flattened pictures which this
		// batch now owns per row. They must not leak into a later prompt.
		st.TakePendingImageParts()
	}
}

// finishQueuedInterrupt returns only unread input. Protected rows survive the
// legacy CloseMessageQueue's steer discard, with their original modes and IDs.
func (st *State) finishQueuedInterrupt(c *queuedTurnInterrupt) {
	st.queueMu.Lock()
	changed := false
	if st.queueInterrupt == c {
		if r := c.pending; r != nil && !r.read.Load() {
			st.queue = append(append([]QueuedMessage(nil), r.rows...), st.queue...)
			if st.queueRetained == nil {
				st.queueRetained = make(map[string]bool)
			}
			for _, q := range r.rows {
				st.queueRetained[q.ID] = true
			}
			st.bumpQueueLocked()
			changed = true
		}
		st.queueInterrupt = nil
	}
	st.queueMu.Unlock()
	if changed {
		st.notifyQueue()
	}
}
