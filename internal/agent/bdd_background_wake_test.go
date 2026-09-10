package agent

// Godog harness for features/background_wake.feature: drives the waker with
// snapshots the pool would emit and records the turns it starts, so the
// scenarios describe the wake contract without a model or a real shell.
//
// Two scenarios deliberately break that isolation, because the isolation is
// what let a lost wake through review: one holds the composer turn lock the way
// a still-running turn does, and one runs a real failing `git submodule update`
// through the real pool, so the outcome the waker receives comes from an actual
// exit code rather than a hand-built snapshot.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type wakeFeatureState struct {
	waker *BackgroundWaker

	mu           sync.Mutex
	instructions []string
	lastStatus   bgtask.Status

	seq int

	// busy stands in for the composer turn lock: while it is set the runner
	// refuses a wake exactly as a session with a turn already in flight does.
	busy     atomic.Bool
	refusals atomic.Int32

	// pool is the real pool the submodule scenario runs its command through.
	pool      *bgtask.Pool
	sessionID string
	taskID    string
	repoDir   string
	tmpRoot   string
}

func (s *wakeFeatureState) reset() {
	s.stopPool()
	s.mu.Lock()
	s.instructions = nil
	s.lastStatus = ""
	s.seq = 0
	s.mu.Unlock()
	s.busy.Store(false)
	s.refusals.Store(0)
	s.sessionID = ""
	s.taskID = ""
	s.repoDir = ""
	s.waker = NewBackgroundWaker(slog.Default(), s.record)
	// A busy session must be retried quickly here; the production backoff would
	// outlast the suite.
	s.waker.busyRetryFirst = 20 * time.Millisecond
	s.waker.busyRetryMax = 50 * time.Millisecond
}

func (s *wakeFeatureState) stopPool() {
	if s.pool != nil && s.sessionID != "" {
		s.pool.StopSession(s.sessionID)
	}
	s.pool = nil
	if s.tmpRoot != "" {
		_ = os.RemoveAll(s.tmpRoot)
		s.tmpRoot = ""
	}
}

func (s *wakeFeatureState) record(_ context.Context, _, instruction string) error {
	if s.busy.Load() {
		s.refusals.Add(1)
		// Wrapped, not returned bare: the fix has to match with errors.Is.
		return fmt.Errorf("background wake: %w", session.ErrSessionTurnBusy)
	}
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
	s.mu.Lock()
	s.lastStatus = status
	s.mu.Unlock()
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

// turnInFlight makes every wake attempt hit the busy turn lock, the way a task
// that dies seconds after the model started it does.
func (s *wakeFeatureState) turnInFlight() error {
	s.busy.Store(true)
	return nil
}

// turnInFlightEnds releases the lock, but only once the waker has actually been
// refused: without that wait the scenario could pass by finishing the turn
// before the first attempt and never exercise the retry at all.
func (s *wakeFeatureState) turnInFlightEnds() error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.refusals.Load() > 0 {
			s.busy.Store(false)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the waker never attempted a turn while the session was busy")
}

// waitForTurns gives the waker its settle window before asserting.
func (s *wakeFeatureState) waitForTurns(want int) []string {
	deadline := time.Now().Add(15 * time.Second)
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
	s.mu.Lock()
	status := string(s.lastStatus)
	s.mu.Unlock()
	if !strings.Contains(got[0], status) || !strings.Contains(got[0], "did not succeed") {
		return fmt.Errorf("woken turn %q does not report %q plainly", got[0], status)
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

// refusedSubmoduleCheckout builds a superproject whose submodules point at an
// SSH remote, plus a stub ssh that refuses the key. Nothing leaves the machine:
// git runs for real, the transport is the stub, and the exit code is git's own.
func (s *wakeFeatureState) refusedSubmoduleCheckout() error {
	root, err := os.MkdirTemp("", "coddy-bdd-submodule-*")
	if err != nil {
		return err
	}
	s.tmpRoot = root

	ssh := filepath.Join(root, "refuse-ssh")
	script := "#!/bin/sh\necho \"git@github.com: Permission denied (publickey).\" >&2\nexit 255\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil { //nolint:gosec // the stub must be executable
		return err
	}

	repo := filepath.Join(root, "drobek-site")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	if err := runGit(repo, "init", "-q"); err != nil {
		return err
	}
	modules := "[submodule \".performance-pipeline\"]\n" +
		"\tpath = .performance-pipeline\n" +
		"\turl = git@github.com:viktor-drobek/drobek-performance-pipeline.git\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitmodules"), []byte(modules), 0o644); err != nil { //nolint:gosec // fixture, not a secret
		return err
	}
	// A gitlink entry is what makes `submodule update --init` attempt a clone;
	// the commit it points at never has to exist locally.
	if err := runGit(repo, "update-index", "--add",
		"--cacheinfo", "160000,0000000000000000000000000000000000000001,.performance-pipeline"); err != nil {
		return err
	}

	s.repoDir = repo
	return nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// runRealTask puts a command through the real pool with the waker subscribed -
// the path the reported bug travelled - and waits for the outcome the waker is
// handed to be the one the OS produced.
func (s *wakeFeatureState) runRealTask(sessionID, command string, timeoutSeconds int, want bgtask.Status) error {
	s.pool = bgtask.New(bgtask.Config{})
	s.waker.Attach(s.pool)
	s.sessionID = sessionID

	snap, err := s.pool.Start(bgtask.Spec{
		SessionID:      sessionID,
		Kind:           bgtask.KindCommand,
		Command:        command,
		CWD:            s.tmpRoot,
		TimeoutSeconds: timeoutSeconds,
		NotifyOnFinish: true,
	})
	if err != nil {
		return err
	}
	s.taskID = snap.ID

	final, err := s.pool.Wait(context.Background(), sessionID, s.taskID, 60*time.Second)
	if err != nil {
		return err
	}
	if final.Status != want {
		return fmt.Errorf("task ended as %q, want %q", final.Status, want)
	}
	s.mu.Lock()
	s.lastStatus = final.Status
	s.mu.Unlock()
	return nil
}

// updatesSubmodulesInBackground reproduces the reported incident verbatim: the
// operator's `git submodule update --init --recursive`, refused by the remote,
// exiting non-zero seconds after the model handed it off.
func (s *wakeFeatureState) updatesSubmodulesInBackground() error {
	if s.repoDir == "" {
		return fmt.Errorf("no submodule checkout was prepared")
	}
	ssh := filepath.Join(s.tmpRoot, "refuse-ssh")
	command := fmt.Sprintf("cd %q && GIT_SSH_COMMAND=%q GIT_TERMINAL_PROMPT=0 git submodule update --init --recursive",
		s.repoDir, ssh)
	return s.runRealTask("bdd-wake-submodule", command, 60, bgtask.StatusFailed)
}

// outlivesItsTimeout drives the other terminal state an unattended task can
// reach: the pool kills it, and that outcome has to reach the model too.
func (s *wakeFeatureState) outlivesItsTimeout() error {
	if s.tmpRoot == "" {
		root, err := os.MkdirTemp("", "coddy-bdd-wake-*")
		if err != nil {
			return err
		}
		s.tmpRoot = root
	}
	return s.runRealTask("bdd-wake-timeout", "sleep 30", 1, bgtask.StatusTimedOut)
}

func (s *wakeFeatureState) outputNamesRefusedClone() error {
	if s.pool == nil {
		return fmt.Errorf("no background task was run")
	}
	out, _, err := s.pool.Output(s.sessionID, s.taskID, 200)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Permission denied (publickey)") {
		return fmt.Errorf("task output does not carry the refusal: %q", out)
	}
	return nil
}

func initializeBackgroundWakeScenario(sc *godog.ScenarioContext) {
	s := &wakeFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.stopPool()
		return ctx, nil
	})

	sc.Step(`^a session with no woken turns$`, s.noWokenTurns)
	sc.Step(`^an agent turn is already in flight for that session$`, s.turnInFlight)
	sc.Step(`^a checkout whose submodule remotes refuse the SSH key$`, s.refusedSubmoduleCheckout)
	sc.Step(`^a background task that asked to be notified finishes as "([^"]*)"$`, s.finishesNotified)
	sc.Step(`^a background task that did not ask to be notified finishes as "([^"]*)"$`, s.finishesQuiet)
	sc.Step(`^three background tasks that asked to be notified finish together$`, s.threeFinishTogether)
	sc.Step(`^the agent updates its submodules as a notifying background task$`, s.updatesSubmodulesInBackground)
	sc.Step(`^a notifying background task outlives its hard timeout$`, s.outlivesItsTimeout)
	sc.Step(`^the turn in flight ends$`, s.turnInFlightEnds)
	sc.Step(`^the agent is woken once$`, s.wokenOnce)
	sc.Step(`^the agent is not woken$`, s.notWoken)
	sc.Step(`^the woken turn names that task and its outcome$`, s.turnNamesTaskAndOutcome)
	sc.Step(`^the woken turn tells the model the work did not succeed$`, s.turnReportsFailure)
	sc.Step(`^the woken turn names all three tasks$`, s.turnNamesAllThree)
	sc.Step(`^the task output names the clone the remote refused$`, s.outputNamesRefusedClone)
}

// wakeFeatureTags drops the scenarios that need a POSIX shell and a real git
// from hosts that have neither, rather than letting them fail for the
// environment. Everywhere the repository is actually checked out - CI included
// - both are present and the scenarios run.
func wakeFeatureTags(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Log("skipping the real-shell scenarios: they need a POSIX shell")
		return "~@real-shell"
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Log("skipping the real-shell scenarios: git is not on PATH")
		return "~@real-shell"
	}
	return ""
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
			Tags:     wakeFeatureTags(t),
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background wake feature suite failed")
	}
}
