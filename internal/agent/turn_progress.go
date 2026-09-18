package agent

// The running turn's clock and token count, published as turn_progress.
//
// A surface shows how long the turn has been running and how much the model
// has written in it. The loop is the one place that sees every delta and
// every provider usage figure, so it counts here and every surface renders
// the same number: the web UI, the console, a console attached to a remote
// server. Design record: docs/plans/turn-progress.md.

import (
	"sync"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// turnProgressInterval is the least time between two updates while a model
// call streams. A second is what a clock on the surface ticks at anyway.
const turnProgressInterval = time.Second

// turnProgressState is the part of the session state that keeps the progress
// for a client joining the turn late (session/turn_progress.go).
type turnProgressState interface {
	BeginTurnProgress(startedAt time.Time)
	SetTurnOutputTokens(tokens int, estimated bool)
	TurnProgress() (session.TurnProgress, bool)
	EndTurnProgress(startedAt time.Time)
}

// turnProgress counts one turn. The stream callback may run on the
// provider's goroutine, so everything behind mu.
type turnProgress struct {
	send      func(acp.TurnProgressUpdate)
	state     turnProgressState
	startedAt time.Time
	// owned says the loop opened the state's progress itself, because nothing
	// admitted the turn through the manager, and so closes it as well.
	owned bool

	mu sync.Mutex
	// settled is the output of the completed calls: what the provider
	// reported, or the estimate for a call it reported nothing for.
	settled          int
	settledEstimated bool
	// inflightRunes is what the call in flight has streamed so far.
	inflightRunes int
	lastSent      time.Time
}

// beginTurnProgress opens the turn's progress and announces its clock. The
// start is the one the manager admitted the turn at; a turn nothing admitted
// (a test, a resumed permission) starts now. A loop that continues a turn
// another agent of the same turn began keeps the count that one reached.
func (a *Agent) beginTurnProgress() {
	if a.progress != nil {
		return
	}
	p := &turnProgress{startedAt: time.Now().UTC()}
	sessionID := a.state.GetID()
	p.send = func(u acp.TurnProgressUpdate) { _ = a.server.SendSessionUpdate(sessionID, u) }
	if st, ok := a.state.(turnProgressState); ok {
		p.state = st
		if running, ok := st.TurnProgress(); ok && !running.StartedAt.IsZero() {
			p.startedAt = running.StartedAt
			p.settled, p.settledEstimated = running.OutputTokens, running.Estimated
		} else {
			st.BeginTurnProgress(p.startedAt)
			p.owned = true
		}
	}
	a.progress = p
	p.publish(time.Now())
}

// endTurnProgress closes a progress this loop opened itself. One the manager
// opened is the manager's to close, when the turn releases.
func (a *Agent) endTurnProgress() {
	p := a.progress
	if p == nil {
		return
	}
	a.progress = nil
	if p.owned && p.state != nil {
		p.state.EndTurnProgress(p.startedAt)
	}
}

// beginCall opens the account of one model call. What an abandoned call had
// streamed - a response the loop guard cut, a stream that broke - was still
// generated, so it stays in the count as an estimate.
func (p *turnProgress) beginCall() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.foldInflight()
}

// streamed counts text the call in flight produced: a text or reasoning
// delta, or the name and arguments of a completed tool call.
func (p *turnProgress) streamed(text string) {
	if p == nil || text == "" {
		return
	}
	now := time.Now()
	p.mu.Lock()
	p.inflightRunes += utf8.RuneCountInString(text)
	due := now.Sub(p.lastSent) >= turnProgressInterval
	p.mu.Unlock()
	if due {
		p.publish(now)
	}
}

// finishCall settles the call in flight on what the provider reported for it.
// A provider that reports nothing leaves the estimate standing.
func (p *turnProgress) finishCall(reportedOutputTokens int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if reportedOutputTokens > 0 {
		p.settled += reportedOutputTokens
		p.inflightRunes = 0
	} else {
		p.foldInflight()
	}
	p.mu.Unlock()
	p.publish(time.Now())
}

// foldInflight moves the estimate of the call in flight into the settled
// count. Callers hold mu.
func (p *turnProgress) foldInflight() {
	if p.inflightRunes == 0 {
		return
	}
	p.settled += estimateTokensFromRunes(p.inflightRunes)
	p.settledEstimated = true
	p.inflightRunes = 0
}

func (p *turnProgress) publish(now time.Time) {
	p.mu.Lock()
	tokens := p.settled + estimateTokensFromRunes(p.inflightRunes)
	estimated := p.settledEstimated || p.inflightRunes > 0
	p.lastSent = now
	p.mu.Unlock()

	if p.state != nil {
		p.state.SetTurnOutputTokens(tokens, estimated)
	}
	elapsed := now.Sub(p.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	p.send(acp.TurnProgressUpdate{
		SessionUpdate: acp.UpdateTypeTurnProgress,
		StartedAt:     p.startedAt.UTC().Format(time.RFC3339Nano),
		ElapsedMs:     elapsed.Milliseconds(),
		OutputTokens:  tokens,
		Estimated:     estimated,
	})
}

// estimateTokensFromRunes is session.EstimateTokens over a rune count: the
// stream is counted in runes as it arrives and turned into tokens once, so a
// thousand one-rune deltas do not round up a thousand times.
func estimateTokensFromRunes(runes int) int {
	if runes <= 0 {
		return 0
	}
	return (runes + 3) / 4
}
