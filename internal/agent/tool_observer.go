package agent

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// The outcomes a tool call ends in, in the order ToolCallOutcomes lists them.
const (
	// ToolOutcomeOK is a call whose tool ran and returned a result.
	ToolOutcomeOK = "ok"
	// ToolOutcomeError is a call whose tool ran and returned an error.
	ToolOutcomeError = "error"
	// ToolOutcomeRefused is a call that never ran: the mode, the session's
	// toolset, a subagent's tool set, a hook or the safety check refused it.
	ToolOutcomeRefused = "refused"
	// ToolOutcomeDenied is a call the person declined at the permission prompt.
	ToolOutcomeDenied = "denied"
)

// The tool label of a call to a tool of an MCP server, and of a name the
// agent does not know (a hallucinated or replayed one).
const (
	toolLabelMCP   = "mcp"
	toolLabelOther = "other"
)

// ToolCallOutcomes lists every outcome a ToolCallEvent can carry, in a fixed
// order, as a slice of its own.
func ToolCallOutcomes() []string {
	return []string{ToolOutcomeOK, ToolOutcomeError, ToolOutcomeRefused, ToolOutcomeDenied}
}

// ToolCallEvent is one tool call the agent loop decided on, in any session
// of the process, a subagent's included. It names the tool only by what the
// binary itself defines: a built-in tool's name, "mcp" for a tool of an MCP
// server and "other" for a name the agent does not know, so no server or tool
// name from outside the binary reaches an observer.
type ToolCallEvent struct {
	Tool    string
	Outcome string
	// Duration is how long the tool ran, hooks excluded; zero for a call
	// that never ran.
	Duration time.Duration
}

type toolObserver struct{ fn func(ToolCallEvent) }

var (
	toolObserversMu sync.Mutex
	// toolObservers is replaced whole on every change, nil while there is no
	// observer, so a call without one costs a single atomic load.
	toolObservers atomic.Pointer[[]*toolObserver]
)

// AddToolCallObserver registers fn for every tool call of every session in
// the process and returns the function that removes it again, which is safe
// to call twice.
//
// fn runs on the goroutine of the turn, after the call's outcome is known, and
// MUST NOT block, like an llm.AddCallObserver observer.
//
// A permission prompt nobody answered (the stream that carried it ended) is no
// outcome: the call is resumed later and observed when it runs.
func AddToolCallObserver(fn func(ToolCallEvent)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	o := &toolObserver{fn: fn}
	swapToolObservers(func(cur []*toolObserver) []*toolObserver { return append(slices.Clip(cur), o) })
	var once sync.Once
	return func() {
		once.Do(func() {
			swapToolObservers(func(cur []*toolObserver) []*toolObserver {
				return slices.DeleteFunc(slices.Clone(cur), func(x *toolObserver) bool { return x == o })
			})
		})
	}
}

func swapToolObservers(change func(cur []*toolObserver) []*toolObserver) {
	toolObserversMu.Lock()
	defer toolObserversMu.Unlock()
	var cur []*toolObserver
	if p := toolObservers.Load(); p != nil {
		cur = *p
	}
	if next := change(cur); len(next) > 0 {
		toolObservers.Store(&next)
	} else {
		toolObservers.Store(nil)
	}
}

func notifyToolCallObservers(ev ToolCallEvent) {
	observers := toolObservers.Load()
	if observers == nil {
		return
	}
	for _, o := range *observers {
		o.fn(ev)
	}
}

// observeToolCall reports the call to name that ended in outcome after running
// for ran; an empty outcome is a call that has none yet.
func (a *Agent) observeToolCall(name, outcome string, ran time.Duration) {
	if outcome == "" || toolObservers.Load() == nil {
		return
	}
	notifyToolCallObservers(ToolCallEvent{Tool: a.toolLabel(name), Outcome: outcome, Duration: ran})
}

// toolLabel is the name a tool call is reported under: a tool the agent's
// registry holds by its own name, any MCP tool as mcp, anything else as other.
func (a *Agent) toolLabel(name string) string {
	if isMCPToolName(name) {
		return toolLabelMCP
	}
	if a.registry != nil {
		if _, ok := a.registry.Get(name); ok {
			return name
		}
	}
	return toolLabelOther
}

// pendingPermissions and pendingQuestions count the prompts of every session
// in the process that wait for a person right now.
var pendingPermissions, pendingQuestions atomic.Int64

// PendingPrompts reports how many permission prompts and questions of the
// process wait for an answer. A prompt counts while the call that asked it
// waits, a subagent's included; one whose stream ended waits in no call and
// is not counted until it is resumed.
func PendingPrompts() (permission, question int) {
	return int(pendingPermissions.Load()), int(pendingQuestions.Load())
}

// waitingFor counts one prompt on gauge until the returned function runs.
func waitingFor(gauge *atomic.Int64) (answered func()) {
	gauge.Add(1)
	var once sync.Once
	return func() { once.Do(func() { gauge.Add(-1) }) }
}
