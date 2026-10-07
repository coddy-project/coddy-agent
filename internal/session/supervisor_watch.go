package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// loopMaxPeriod is the longest cycle of tool operations the watchdog looks
// for: one call repeated, a pair (edit, test, edit, test) or a triple.
const loopMaxPeriod = 3

// turnWatch is the sender a supervised turn runs with. It forwards every
// update and watches them: when nothing happens for the stall timeout, or the
// same cycle of tool operations comes round loop_repeat times, it cancels the
// step and records why. It also sums the tokens the turn spends.
type turnWatch struct {
	acp.UpdateSender
	state  *State
	cancel context.CancelFunc
	stall  time.Duration
	repeat int

	mu sync.Mutex
	// last is the moment of the latest activity.
	last time.Time
	// waiting counts permission and question prompts a person has open.
	waiting int
	// running is the set of tool calls executing now: a tool has its own
	// timeout, so the stall timer waits for it.
	running map[string]bool
	// ops is the fingerprint sequence of the finished tool operations.
	ops       []string
	seen      map[string]bool
	calls     int
	tokenSum  int
	stoppedBy string
}

func newTurnWatch(sender acp.UpdateSender, state *State, cancel context.CancelFunc, stall time.Duration, repeat int) *turnWatch {
	return &turnWatch{UpdateSender: sender, state: state, cancel: cancel, stall: stall, repeat: repeat,
		last: time.Now(), running: make(map[string]bool), seen: make(map[string]bool)}
}

// activity says whether an update is a sign of the turn moving. The clock
// and usage frames are sent on a timer or by other clients and prove
// nothing about the turn.
func activity(update interface{}) bool {
	switch update.(type) {
	case acp.TurnProgressUpdate, acp.UsageUpdate, acp.ProviderUsageUpdate,
		acp.MessageQueueUpdate, acp.SessionGoalUpdate, acp.SessionSettingsUpdate:
		return false
	}
	return true
}

func (w *turnWatch) SendSessionUpdate(sessionID string, update interface{}) error {
	var op string
	if event, ok := update.(acp.ToolCallStatusUpdate); ok && (event.Status == "completed" || event.Status == "failed") {
		op = w.fingerprint(event)
	}
	w.mu.Lock()
	if activity(update) {
		w.last = time.Now()
	}
	switch event := update.(type) {
	case acp.TokenUsageUpdate:
		w.tokenSum += max(event.InputTokens-event.CachedInputTokens, 0) + event.OutputTokens
	case acp.ToolCallStatusUpdate:
		switch event.Status {
		case "in_progress":
			w.running[event.ToolCallID] = true
		case "completed", "failed", "cancelled":
			delete(w.running, event.ToolCallID)
			if !w.seen[event.ToolCallID] && event.Status != "cancelled" {
				w.seen[event.ToolCallID] = true
				w.calls++
				if op != "" {
					w.ops = append(w.ops, op)
					w.checkLoop()
				}
			}
		}
	}
	w.mu.Unlock()
	return w.UpdateSender.SendSessionUpdate(sessionID, update)
}

// checkLoop cancels the step when the tail of the operation sequence is one
// cycle of period 1..loopMaxPeriod repeated w.repeat times. Called with w.mu.
func (w *turnWatch) checkLoop() {
	if w.repeat <= 0 || w.stoppedBy != "" {
		return
	}
	for period := 1; period <= loopMaxPeriod; period++ {
		need := period * w.repeat
		// A single call repeated needs w.repeat occurrences; a longer cycle
		// needs at least two full rounds whatever the setting says.
		if period > 1 {
			need = period * max(w.repeat, 2)
		}
		if len(w.ops) < need {
			continue
		}
		tail := w.ops[len(w.ops)-need:]
		cycle := true
		for i := period; i < len(tail); i++ {
			if tail[i] != tail[i-period] {
				cycle = false
				break
			}
		}
		if cycle && distinctOps(tail[:period]) == period {
			w.stoppedBy = "repeating the same tool operations with the same results"
			w.cancel()
			return
		}
	}
}

func distinctOps(ops []string) int {
	seen := make(map[string]bool, len(ops))
	for _, op := range ops {
		seen[op] = true
	}
	return len(seen)
}

// fingerprint is one finished tool operation: the call's name and canonical
// arguments and a hash of what it returned. The same call with a different
// result (a test that now fails elsewhere, a file that changed) is progress
// and does not count.
func (w *turnWatch) fingerprint(event acp.ToolCallStatusUpdate) string {
	call, ok := w.findCall(event.ToolCallID)
	if !ok {
		return ""
	}
	var result strings.Builder
	result.WriteString(event.Status)
	for _, item := range event.Content {
		result.WriteString("\x00")
		result.WriteString(item.Content.Text)
	}
	sum := sha256.Sum256([]byte(result.String()))
	return call.Name + "\x00" + canonicalArgs(call.InputJSON) + "\x00" + hex.EncodeToString(sum[:8])
}

func (w *turnWatch) findCall(id string) (llm.ToolCall, bool) {
	w.state.mu.RLock()
	defer w.state.mu.RUnlock()
	for i := len(w.state.Messages) - 1; i >= 0; i-- {
		for _, call := range w.state.Messages[i].ToolCalls {
			if call.ID == id {
				return call, true
			}
		}
	}
	return llm.ToolCall{}, false
}

func canonicalArgs(input string) string {
	var args interface{}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return strings.TrimSpace(input)
	}
	out, _ := json.Marshal(args)
	return string(out)
}

func (w *turnWatch) RequestPermission(ctx context.Context, req acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	w.setWaiting(1)
	defer w.setWaiting(-1)
	return w.UpdateSender.RequestPermission(ctx, req)
}

func (w *turnWatch) RequestQuestion(ctx context.Context, req acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	w.setWaiting(1)
	defer w.setWaiting(-1)
	return w.UpdateSender.RequestQuestion(ctx, req)
}

func (w *turnWatch) setWaiting(delta int) {
	w.mu.Lock()
	w.waiting += delta
	w.last = time.Now()
	w.mu.Unlock()
}

// cause is why the watch cancelled the step, empty when it did not.
func (w *turnWatch) cause() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stoppedBy
}

// toolCalls counts the tool calls that finished during the step.
func (w *turnWatch) toolCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// tokens is what the step spent: uncached input plus output.
func (w *turnWatch) tokens() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tokenSum
}

// run is the stall timer. A person deciding, a tool executing or background
// work in flight is not a stall: the timer starts over when they end.
func (w *turnWatch) run(ctx context.Context, done <-chan struct{}) {
	if w.stall <= 0 {
		return
	}
	interval := min(w.stall/4, time.Second)
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			w.mu.Lock()
			busy := w.waiting > 0 || len(w.running) > 0
			w.mu.Unlock()
			if !busy && runningBackgroundWork(w.state.ID) {
				busy = true
			}
			w.mu.Lock()
			if busy {
				w.last = time.Now()
			} else if time.Since(w.last) >= w.stall && w.stoppedBy == "" {
				w.stoppedBy = "stalled: nothing happened for " + w.stall.String()
				w.cancel()
			}
			w.mu.Unlock()
		}
	}
}
