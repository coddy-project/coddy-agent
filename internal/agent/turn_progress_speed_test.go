package agent

import (
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestNewModelCallHidesThePreviousSpeedBeforeItsFirstToken(t *testing.T) {
	speed := 9.8
	var last acp.TurnProgressUpdate
	p := &turnProgress{startedAt: time.Now(), speed: generationSpeed{current: &speed},
		send: func(u acp.TurnProgressUpdate) { last = u }}
	p.beginCall(true)
	if last.StartedAt == "" || last.TokensPerSecond != nil {
		t.Fatalf("new call did not clear the visible window: %+v", last)
	}
}

func TestContinuedTurnReplacesItsGenerationSummary(t *testing.T) {
	state := &session.State{ID: "sess_continued_speed"}
	start := time.Now().Add(-time.Hour)
	state.BeginTurnProgress(start)
	for _, tokens := range []int{100, 200} {
		a := &Agent{state: state, server: &progressSender{}}
		a.beginTurnProgress()
		a.progress.beginCall(true)
		a.progress.mu.Lock()
		a.progress.inflightRunes = tokens * 4
		a.progress.speed.add(1, start)
		a.progress.speed.add(tokens, start.Add(2*time.Second))
		a.progress.mu.Unlock()
		a.endTurnProgress()
	}
	rows := state.GetUILog()
	if len(rows) != 1 {
		t.Fatalf("one admitted turn produced %d generation summaries, want 1", len(rows))
	}
	if got := rows[0].Generation; got == nil || got.OutputTokens != 300 || got.TokensPerSecond != 75 {
		t.Fatalf("continued turn summary = %+v, want 300 tokens at 75 tok/s", got)
	}
}

func TestInterruptedTurnKeepsGenerationAverage(t *testing.T) {
	// A cancellation and a provider error both leave a partially streamed call.
	// The deferred turn close must settle that call and publish its average.
	state := &session.State{ID: "sess_speed"}
	start := time.Now().Add(-time.Hour)
	state.BeginTurnProgress(start)
	sender := &progressSender{}
	a := &Agent{state: state, server: sender}
	a.beginTurnProgress()
	a.progress.beginCall(true)
	a.progress.mu.Lock()
	a.progress.inflightRunes = 400
	a.progress.speed.add(1, start)
	a.progress.speed.add(100, start.Add(2*time.Second))
	a.progress.mu.Unlock()
	a.endTurnProgress()
	updates, _ := sender.progress()
	last := updates[len(updates)-1]
	if !last.Completed || last.OutputTokens != 100 || !last.Estimated {
		t.Fatalf("partial final progress = %+v", last)
	}
	requireSpeed(t, last.TokensPerSecond, 50)
	rows := state.GetUILog()
	if len(rows) != 1 || rows[0].Generation == nil || rows[0].Generation.TokensPerSecond != 50 {
		t.Fatalf("generation summary missing: %+v", rows)
	}
}
