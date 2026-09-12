//go:build cli

package cli

// The scripted-turn directives of the TUI harness are one buffered channel
// shared by every turn, so a directive that outlives the turn it was written
// for is picked up by the next one. These tests pin the handshake that stops
// that from happening.

import (
	"testing"
	"time"
)

// A block directive is the dangerous one: the turn that takes it waits on a
// channel only the cancelling step closes. Leaked into a later turn, that turn
// waits forever - the console sits on "Waiting for the model" and every
// directive queued behind it is stranded, which is how this reached CI as a
// load-sensitive failure of "Starting a new session drops updates from the old
// one".
func TestStubBlockStepWaitsUntilTheTurnTakesTheDirective(t *testing.T) {
	s := &cliTUIState{directives: make(chan stubDirective, 16)}

	// A consumer that arrives later than the step's own patience, the way a
	// loaded machine does.
	const consumerDelay = 300 * time.Millisecond
	consumed := make(chan stubDirective, 1)
	go func() {
		time.Sleep(consumerDelay)
		d := <-s.directives
		if d.taken != nil {
			close(d.taken)
		}
		consumed <- d
	}()

	start := time.Now()
	if err := s.stubBlocksUntilCancelled(); err != nil {
		t.Fatalf("stubBlocksUntilCancelled: %v", err)
	}
	elapsed := time.Since(start)

	// The consumer closes d.taken (which releases the step) before it reports
	// on consumed, so the step may legitimately return a moment before the
	// report lands: wait for it instead of peeking.
	select {
	case d := <-consumed:
		if d.kind != "block" {
			t.Fatalf("consumed directive kind = %q, want block", d.kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("the step returned after %v with the block directive still queued; "+
			"a later turn would take it and wait on a channel nobody closes", elapsed)
	}
	if len(s.directives) != 0 {
		t.Fatalf("%d directive(s) left queued after the step returned", len(s.directives))
	}
}

// The step must fail rather than hang when nothing ever takes the directive,
// so a broken harness reports itself instead of burning the suite timeout.
func TestStubBlockStepFailsWhenNoTurnTakesTheDirective(t *testing.T) {
	s := &cliTUIState{directives: make(chan stubDirective, 16)}
	prev := stubBlockTakeTimeout
	stubBlockTakeTimeout = 150 * time.Millisecond
	defer func() { stubBlockTakeTimeout = prev }()

	if err := s.stubBlocksUntilCancelled(); err == nil {
		t.Fatal("expected an error when no turn consumes the block directive")
	}
}
