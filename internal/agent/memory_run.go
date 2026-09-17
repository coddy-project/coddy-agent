package agent

// The memory subagent from the parent's side: the per-turn bookkeeping of the
// run a user turn started, the delivery of its report to the main model, the
// bounds on runs in flight, and the retention of finished runs. What needs
// the memory module itself (the template, the tools, the launch) lives in
// memory_hooks.go behind the memory build tag; everything here is untagged so
// the loop, the turn context and the surfaces can reach it in any build.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

const (
	// memoryMaxInFlight bounds the memory runs of one session that may
	// overlap: turns are serialised by the turn lock, so overlap only
	// happens when turns end faster than memory runs do.
	memoryMaxInFlight = 2
	// memoryMaxInFlightProcess bounds them across the process, so a server
	// with many busy sessions cannot fan out without limit against one
	// provider.
	memoryMaxInFlightProcess = 16
	// MemoryDrainGrace is how long a surface that stops the pool waits for
	// running memory runs first: a run stopped mid-persist loses its note.
	MemoryDrainGrace = 15 * time.Second
	// memoryTaskLabelPrefix opens the label of a memory task.
	memoryTaskLabelPrefix = "memory: "
)

// memoryInFlight counts the memory runs in flight, per session and in total.
var memoryInFlight = struct {
	mu      sync.Mutex
	session map[string]int
	total   int
}{session: map[string]int{}}

// acquireMemorySlot reserves a slot for a memory run of the session, or says
// why none is free. The release is idempotent.
func acquireMemorySlot(sessionID string) (release func(), reason string) {
	memoryInFlight.mu.Lock()
	defer memoryInFlight.mu.Unlock()
	if memoryInFlight.session[sessionID] >= memoryMaxInFlight {
		return nil, fmt.Sprintf("memory runs in flight for this session: %d of %d", memoryInFlight.session[sessionID], memoryMaxInFlight)
	}
	if memoryInFlight.total >= memoryMaxInFlightProcess {
		return nil, fmt.Sprintf("memory runs in flight across the process: %d of %d", memoryInFlight.total, memoryMaxInFlightProcess)
	}
	memoryInFlight.session[sessionID]++
	memoryInFlight.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			memoryInFlight.mu.Lock()
			defer memoryInFlight.mu.Unlock()
			if memoryInFlight.session[sessionID] > 1 {
				memoryInFlight.session[sessionID]--
			} else {
				delete(memoryInFlight.session, sessionID)
			}
			if memoryInFlight.total > 0 {
				memoryInFlight.total--
			}
		})
	}, ""
}

// MemoryRunsInFlight reports how many memory runs the process has running.
func MemoryRunsInFlight() int {
	memoryInFlight.mu.Lock()
	defer memoryInFlight.mu.Unlock()
	return memoryInFlight.total
}

// WaitMemoryRuns blocks until no memory run is in flight, the grace elapses
// or ctx ends, and reports whether every run settled. A surface that is about
// to stop the pool calls it first, so a persist in flight is not cut.
func WaitMemoryRuns(ctx context.Context, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for MemoryRunsInFlight() > 0 {
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return MemoryRunsInFlight() == 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// memoryTurnRun is the memory subagent of one user turn as its parent sees
// it. The Agent lives for one turn, so the field that holds it needs no
// clearing: the next turn is a new Agent.
type memoryTurnRun struct {
	mu        sync.Mutex
	parentID  string
	taskID    string
	childID   string
	run       *subagentRun
	pool      *bgtask.Pool
	startedAt time.Time
	// delivered says a non-empty report reached the model in this turn.
	delivered bool
	// settled says the delivery was decided and the finished update sent.
	settled bool
	// turnOver says the parent turn returned; nothing is delivered after.
	turnOver bool
}

// memoryTaskLabel is the task label and child title of a memory run: the
// first line of the user message behind the prefix, cut like every label.
func memoryTaskLabel(userText string) string {
	return capRunes(memoryTaskLabelPrefix+firstLine(userText), maxTaskLabelRunes)
}

// sendMemoryRun publishes one memory_run update on the parent's stream.
func (a *Agent) sendMemoryRun(u acp.MemoryRunUpdate) {
	if a == nil || a.server == nil {
		return
	}
	u.SessionUpdate = acp.UpdateTypeMemoryRun
	_ = a.server.SendSessionUpdate(a.state.GetID(), u)
}

// memoryRunNote appends a line about the run to its task log, after the
// report block when the run already settled; the sink keeps the record on
// disk in step with the window it serves.
func (mr *memoryTurnRun) note(line string) {
	if mr == nil || mr.run == nil || mr.run.out == nil {
		return
	}
	_, _ = io.WriteString(mr.run.out, line+"\n")
}

// reportText is the child's last assistant message, the value the run
// goroutine recorded before the task settled.
func (mr *memoryTurnRun) reportText() string {
	if mr == nil || mr.run == nil {
		return ""
	}
	mr.run.mu.Lock()
	defer mr.run.mu.Unlock()
	return strings.TrimSpace(mr.run.report)
}

// finishedUpdate is the memory_run update a settled run publishes.
func (mr *memoryTurnRun) finishedUpdate(snap bgtask.Snapshot) acp.MemoryRunUpdate {
	u := acp.MemoryRunUpdate{
		Status:         "finished",
		TaskID:         mr.taskID,
		ChildSessionID: mr.childID,
		TaskStatus:     string(snap.Status),
		DurationMs:     time.Since(mr.startedAt).Milliseconds(),
		Delivered:      mr.delivered,
		Reason:         snap.Error,
	}
	if snap.FinishedAt != nil && !snap.StartedAt.IsZero() {
		u.DurationMs = snap.FinishedAt.Sub(snap.StartedAt).Milliseconds()
	}
	return u
}

// deliverMemoryReport hands the report to the main model when the run has
// settled and nothing was delivered yet: the store the system prompt and the
// turn context read is filled once, the task log says where the report went,
// and the finished update goes out. via names the place the report reaches
// the model through. It reports whether a report is in the store.
func (a *Agent) deliverMemoryReport(via string) bool {
	mr := a.memoryRun
	if mr == nil {
		return false
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if mr.settled || mr.turnOver {
		return mr.delivered
	}
	snap, err := mr.pool.Get(mr.parentID, mr.taskID)
	if err != nil || !snap.Status.Finished() {
		return false
	}
	report := mr.reportText()
	switch {
	case snap.Status != bgtask.StatusSucceeded:
		mr.note(fmt.Sprintf("report not delivered: the run %s", snap.Status))
		a.log.Warn("memory run did not succeed; the turn continues without its report",
			"session_id", mr.parentID, "task", mr.taskID, "child", mr.childID, "status", snap.Status, "error", snap.Error)
	case report == "":
		mr.note("report not delivered: the run produced no final message")
	default:
		a.state.SetMemoryCopilotBlock(report)
		mr.delivered = true
		mr.note("report delivered to the turn (" + via + ")")
		a.log.Info("memory report delivered", "session_id", mr.parentID, "task", mr.taskID, "via", via, "duration_ms", time.Since(mr.startedAt).Milliseconds())
	}
	mr.settled = true
	a.sendMemoryRun(mr.finishedUpdate(snap))
	return mr.delivered
}

// finishMemoryTurn marks the parent turn over. A run that already settled
// needs nothing; one that finished without a delivery is settled now with
// the reason; one still running gets a line in its log saying the turn is
// gone and sends nothing more: the turn's sender does not outlive the turn.
// It runs on the turn's goroutine, before the turn's stream is closed.
func (a *Agent) finishMemoryTurn() {
	mr := a.memoryRun
	if mr == nil {
		return
	}
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if mr.turnOver {
		return
	}
	mr.turnOver = true
	if mr.settled {
		return
	}
	snap, err := mr.pool.Get(mr.parentID, mr.taskID)
	if err == nil && snap.Status.Finished() {
		mr.note("turn ended before the report")
		mr.settled = true
		a.sendMemoryRun(mr.finishedUpdate(snap))
		return
	}
	mr.note("turn ended before the report; the run goes on")
}

// memoryTurnContextSection is the turn context's view of the report: a late
// report is delivered here on every step, and the section is rendered only
// while the store holds text the frozen system prompt does not carry.
func (a *Agent) memoryTurnContextSection(frozen *systemPromptBuild) string {
	if a.memoryRun == nil {
		return ""
	}
	a.deliverMemoryReport("turn context")
	store := strings.TrimSpace(a.state.GetMemoryCopilotBlock())
	if store == "" || (frozen != nil && frozen.MemoryRecall == store) {
		return ""
	}
	return "## Long-term memory\n\n" + store
}
