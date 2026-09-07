package agent

// Godog harness for features/background_wake.feature: drives the waker with
// snapshots the pool would emit and records the turns it starts, so the
// scenarios describe the wake contract without a model or a real shell.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

type wakeFeatureState struct {
	waker *BackgroundWaker

	mu           sync.Mutex
	instructions []string

	seq int
}

func (s *wakeFeatureState) reset() {
	s.mu.Lock()
	s.instructions = nil
	s.seq = 0
	s.mu.Unlock()
	s.waker = NewBackgroundWaker(slog.Default(), s.record)
}

func (s *wakeFeatureState) record(_ context.Context, _, instruction string) error {
	s.mu.Lock()
	s.instructions = append(s.instructions, instruction)
	s.mu.Unlock()
	return nil
}

func (s *wakeFeatureState) turns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.instructions...)
}

func (s *wakeFeatureState) noWokenTurns() error {
	s.reset()
	if len(s.turns()) != 0 {
		return fmt.Errorf("fresh state already recorded turns")
	}
	return nil
}

func (s *wakeFeatureState) emit(status bgtask.Status, notify bool) {
	s.seq++
	end := time.Now()
	code := 0
	if status == bgtask.StatusFailed {
		code = 2
	}
	s.waker.OnSnapshot(bgtask.Snapshot{
		ID:             fmt.Sprintf("bg_%d", s.seq),
		SessionID:      "bdd-wake",
		Kind:           bgtask.KindCommand,
		Label:          fmt.Sprintf("task %d", s.seq),
		Status:         status,
		StartedAt:      end.Add(-20 * time.Second),
		FinishedAt:     &end,
		ExitCode:       &code,
		NotifyOnFinish: notify,
	})
}

func (s *wakeFeatureState) finishesNotified(status string) error {
	s.emit(bgtask.Status(status), true)
	return nil
}

func (s *wakeFeatureState) finishesQuiet(status string) error {
	s.emit(bgtask.Status(status), false)
	return nil
}

func (s *wakeFeatureState) threeFinishTogether() error {
	for range 3 {
		s.emit(bgtask.StatusSucceeded, true)
	}
	return nil
}

// waitForTurns gives the waker its settle window before asserting.
func (s *wakeFeatureState) waitForTurns(want int) []string {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.turns(); len(got) >= want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return s.turns()
}

func (s *wakeFeatureState) wokenOnce() error {
	s.waitForTurns(1)
	// Let a second turn appear if the batching is wrong.
	time.Sleep(wakeSettleDelay + 300*time.Millisecond)
	if got := s.turns(); len(got) != 1 {
		return fmt.Errorf("agent was woken %d times, want exactly 1", len(got))
	}
	return nil
}

func (s *wakeFeatureState) notWoken() error {
	time.Sleep(wakeSettleDelay + 400*time.Millisecond)
	if got := s.turns(); len(got) != 0 {
		return fmt.Errorf("agent was woken %d times, want none", len(got))
	}
	return nil
}

func (s *wakeFeatureState) turnNamesTaskAndOutcome() error {
	got := s.turns()
	if len(got) == 0 {
		return fmt.Errorf("no woken turn recorded")
	}
	for _, want := range []string{"bg_1", "succeeded", "task 1"} {
		if !strings.Contains(got[0], want) {
			return fmt.Errorf("woken turn %q does not mention %q", got[0], want)
		}
	}
	return nil
}

func (s *wakeFeatureState) turnReportsFailure() error {
	got := s.turns()
	if len(got) == 0 {
		return fmt.Errorf("no woken turn recorded")
	}
	if !strings.Contains(got[0], "failed") || !strings.Contains(got[0], "did not succeed") {
		return fmt.Errorf("woken turn %q does not report the failure plainly", got[0])
	}
	return nil
}

func (s *wakeFeatureState) turnNamesAllThree() error {
	got := s.turns()
	if len(got) == 0 {
		return fmt.Errorf("no woken turn recorded")
	}
	for _, id := range []string{"bg_1", "bg_2", "bg_3"} {
		if !strings.Contains(got[0], id) {
			return fmt.Errorf("woken turn %q is missing %s", got[0], id)
		}
	}
	return nil
}

func initializeBackgroundWakeScenario(sc *godog.ScenarioContext) {
	s := &wakeFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.Step(`^a session with no woken turns$`, s.noWokenTurns)
	sc.Step(`^a background task that asked to be notified finishes as "([^"]*)"$`, s.finishesNotified)
	sc.Step(`^a background task that did not ask to be notified finishes as "([^"]*)"$`, s.finishesQuiet)
	sc.Step(`^three background tasks that asked to be notified finish together$`, s.threeFinishTogether)
	sc.Step(`^the agent is woken once$`, s.wokenOnce)
	sc.Step(`^the agent is not woken$`, s.notWoken)
	sc.Step(`^the woken turn names that task and its outcome$`, s.turnNamesTaskAndOutcome)
	sc.Step(`^the woken turn tells the model the work did not succeed$`, s.turnReportsFailure)
	sc.Step(`^the woken turn names all three tasks$`, s.turnNamesAllThree)
}

func TestBackgroundWakeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "background-wake",
		ScenarioInitializer: initializeBackgroundWakeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/background_wake.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background wake feature suite failed")
	}
}
