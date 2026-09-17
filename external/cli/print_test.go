//go:build cli

package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A one-shot print keeps the process alive while its memory run is still
// going: the drain grace first, then the run's own timeout, with one line on
// stderr in between so the wait is visible.
func TestWaitForMemoryRunWaitsOutTheRun(t *testing.T) {
	origInFlight, origWait := memoryRunsInFlight, waitMemoryRuns
	t.Cleanup(func() { memoryRunsInFlight, waitMemoryRuns = origInFlight, origWait })

	var waits []time.Duration
	memoryRunsInFlight = func() int { return 1 }
	waitMemoryRuns = func(_ context.Context, grace time.Duration) bool {
		waits = append(waits, grace)
		// The grace elapses with the run still going; the long wait sees it out.
		return len(waits) > 1
	}
	cfg := &config.Config{}
	cfg.Memory.TimeoutSeconds = 120
	var errOut bytes.Buffer
	waitForMemoryRun(context.Background(), cfg, &errOut)

	if len(waits) != 2 || waits[0] != agent.MemoryDrainGrace || waits[1] != 120*time.Second {
		t.Fatalf("waits = %v, want the drain grace and then the run's timeout", waits)
	}
	if got := errOut.String(); got != "waiting for the memory subagent to finish before exiting\n" {
		t.Fatalf("stderr = %q", got)
	}

	// Nothing in flight: no wait, no line.
	waits = nil
	errOut.Reset()
	memoryRunsInFlight = func() int { return 0 }
	waitForMemoryRun(context.Background(), cfg, &errOut)
	if len(waits) != 0 || errOut.Len() != 0 {
		t.Fatalf("with no run in flight the print must not wait or write, got waits=%v stderr=%q", waits, errOut.String())
	}
}
