package bgtask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// stubRunner keeps the pool tests off the host shell: a task finishes when the
// test releases it, so timing is decided by the test rather than by a sleep.
type stubRunner struct {
	mu       sync.Mutex
	handles  []*stubHandle
	startErr error
}

func (r *stubRunner) Start(spec Spec, out io.Writer) (Handle, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}
	h := &stubHandle{release: make(chan struct{}), out: out}
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	if spec.Label == "writes" {
		_, _ = io.WriteString(out, "hello from stub\n")
	}
	return h, nil
}

func (r *stubRunner) last() *stubHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handles) == 0 {
		return nil
	}
	return r.handles[len(r.handles)-1]
}

type stubHandle struct {
	release  chan struct{}
	out      io.Writer
	once     sync.Once
	exitCode int
	stopped  bool
	mu       sync.Mutex
}

func (h *stubHandle) Wait() (int, error) {
	<-h.release
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitCode, nil
}

// PID is 0 for the stub: it runs no OS process, which is also the shape a
// future non-command runner will have.
func (h *stubHandle) PID() int { return 0 }

func (h *stubHandle) Stop(time.Duration) error {
	h.mu.Lock()
	h.stopped = true
	h.exitCode = 143
	h.mu.Unlock()
	h.finish(143)
	return nil
}

func (h *stubHandle) finish(code int) {
	h.once.Do(func() {
		h.mu.Lock()
		h.exitCode = code
		h.mu.Unlock()
		close(h.release)
	})
}

func newTestPool(t *testing.T, runner Runner, cfg Config) *Pool {
	t.Helper()
	p := NewWithRunner(cfg, runner)
	t.Cleanup(func() { p.StopSession("s1") })
	return p
}

func waitForStatus(t *testing.T, p *Pool, sessionID, taskID string, want Status) Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last Snapshot
	for time.Now().Before(deadline) {
		snap, err := p.Get(sessionID, taskID)
		if err != nil {
			t.Fatalf("Get(%q): %v", taskID, err)
		}
		last = snap
		if snap.Status == want {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s stayed %q, want %q", taskID, last.Status, want)
	return last
}

func TestResolveTimeoutSeconds(t *testing.T) {
	cfg := Config{DefaultTimeoutSeconds: 900, MaxTimeoutSeconds: 3600}.normalised()

	cases := []struct {
		name string
		spec Spec
		want int
	}{
		{name: "explicit timeout wins over estimate", spec: Spec{TimeoutSeconds: 5, ExpectedSeconds: 600}, want: 5},
		{name: "no estimate falls back to default", spec: Spec{}, want: 900},
		{name: "small estimate is floored", spec: Spec{ExpectedSeconds: 4}, want: minEstimatedTimeoutSeconds},
		{name: "estimate buys a multiple of itself", spec: Spec{ExpectedSeconds: 120}, want: 360},
		{name: "estimate is capped by the ceiling", spec: Spec{ExpectedSeconds: 100000}, want: 3600},
		{name: "explicit timeout is capped by the ceiling", spec: Spec{TimeoutSeconds: 100000}, want: 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTimeoutSeconds(tc.spec, cfg); got != tc.want {
				t.Fatalf("resolveTimeoutSeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSnapshotElapsedAndOverdue(t *testing.T) {
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	finish := start.Add(30 * time.Second)

	running := Snapshot{Status: StatusRunning, StartedAt: start, ExpectedSeconds: 10}
	if got := running.Elapsed(start.Add(25 * time.Second)); got != 25*time.Second {
		t.Fatalf("Elapsed() = %v, want 25s", got)
	}
	if !running.Overdue(start.Add(25 * time.Second)) {
		t.Fatal("a running task past its estimate should be overdue")
	}
	if running.Overdue(start.Add(5 * time.Second)) {
		t.Fatal("a running task inside its estimate should not be overdue")
	}

	noEstimate := Snapshot{Status: StatusRunning, StartedAt: start}
	if noEstimate.Overdue(start.Add(time.Hour)) {
		t.Fatal("a task without an estimate can never be overdue")
	}

	done := Snapshot{Status: StatusSucceeded, StartedAt: start, FinishedAt: &finish, ExpectedSeconds: 1}
	if got := done.Elapsed(start.Add(time.Hour)); got != 30*time.Second {
		t.Fatalf("Elapsed() after finish = %v, want 30s", got)
	}
	if done.Overdue(start.Add(time.Hour)) {
		t.Fatal("a finished task is never overdue")
	}
}

func TestPoolRunsTaskToSuccess(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "make build", Label: "writes", ExpectedSeconds: 30})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if snap.Status != StatusRunning {
		t.Fatalf("status = %q, want running", snap.Status)
	}
	if snap.ID == "" {
		t.Fatal("Start() returned an empty task id")
	}

	runner.last().finish(0)
	final := waitForStatus(t, p, "s1", snap.ID, StatusSucceeded)

	if final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", final.ExitCode)
	}
	if final.FinishedAt == nil {
		t.Fatal("a finished task must record FinishedAt")
	}
	out, _, err := p.Output("s1", snap.ID, 0)
	if err != nil {
		t.Fatalf("Output(): %v", err)
	}
	if !strings.Contains(out, "hello from stub") {
		t.Fatalf("output %q does not contain the command output", out)
	}
}

func TestPoolReportsNonZeroExitAsFailed(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "false"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	runner.last().finish(2)

	final := waitForStatus(t, p, "s1", snap.ID, StatusFailed)
	if final.ExitCode == nil || *final.ExitCode != 2 {
		t.Fatalf("exit code = %v, want 2", final.ExitCode)
	}
}

func TestPoolStopMarksTaskStopped(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	stopped, err := p.Stop("s1", snap.ID)
	if err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if stopped.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped", stopped.Status)
	}
	if !runner.last().stopped {
		t.Fatal("Stop() did not reach the handle")
	}
}

func TestPoolTimeoutTerminatesTask(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600", TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if snap.TimeoutSeconds != 1 {
		t.Fatalf("TimeoutSeconds = %d, want 1", snap.TimeoutSeconds)
	}

	final := waitForStatus(t, p, "s1", snap.ID, StatusTimedOut)
	if final.FinishedAt == nil {
		t.Fatal("a timed-out task must record FinishedAt")
	}
}

func TestPoolRejectsWorkOverTheConcurrencyLimit(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{MaxConcurrent: 2})

	for i := range 2 {
		if _, err := p.Start(Spec{SessionID: "s1", Command: fmt.Sprintf("sleep %d", i)}); err != nil {
			t.Fatalf("Start(%d): %v", i, err)
		}
	}
	if _, err := p.Start(Spec{SessionID: "s1", Command: "sleep 3"}); err == nil {
		t.Fatal("Start() past the limit should fail")
	}

	// Another session has its own budget.
	if _, err := p.Start(Spec{SessionID: "s2", Command: "sleep 4"}); err != nil {
		t.Fatalf("Start() for a second session: %v", err)
	}
	p.StopSession("s2")
}

func TestPoolIsolatesSessions(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	a, err := p.Start(Spec{SessionID: "s1", Command: "one"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if _, err := p.Start(Spec{SessionID: "s2", Command: "two"}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() { p.StopSession("s2") })

	if got := len(p.List("s1")); got != 1 {
		t.Fatalf("List(s1) returned %d tasks, want 1", got)
	}
	if _, err := p.Get("s2", a.ID); err == nil {
		t.Fatal("a task id from one session must not resolve in another")
	}
}

func TestPoolWaitReturnsRunningSnapshotOnTimeout(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	waited, err := p.Wait(context.Background(), "s1", snap.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait(): %v", err)
	}
	if waited.Status != StatusRunning {
		t.Fatalf("status = %q, want running after a short wait", waited.Status)
	}
}

func TestPoolStartFailureIsRecordedAsFailedTask(t *testing.T) {
	runner := &stubRunner{startErr: fmt.Errorf("no such shell")}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "whatever"})
	if err == nil {
		t.Fatal("Start() should surface the runner error")
	}
	if snap.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", snap.Status)
	}
	if !strings.Contains(snap.Error, "no such shell") {
		t.Fatalf("error %q does not mention the runner failure", snap.Error)
	}
}

func TestPoolNotifiesSubscribers(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	var mu sync.Mutex
	var seen []Status
	p.Subscribe(func(s Snapshot) {
		mu.Lock()
		seen = append(seen, s.Status)
		mu.Unlock()
	})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "make"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	runner.last().finish(0)
	waitForStatus(t, p, "s1", snap.ID, StatusSucceeded)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 || seen[0] != StatusRunning || seen[len(seen)-1] != StatusSucceeded {
		t.Fatalf("subscriber saw %v, want running then succeeded", seen)
	}
}

func TestOutputSinkKeepsBoundedWindow(t *testing.T) {
	sink := NewOutputSink(16)
	for i := range 10 {
		if _, err := fmt.Fprintf(sink, "line%d\n", i); err != nil {
			t.Fatalf("Write(): %v", err)
		}
	}

	total, truncated := sink.Stats()
	if total != 60 {
		t.Fatalf("total = %d, want 60", total)
	}
	if !truncated {
		t.Fatal("a window smaller than the output must report truncation")
	}
	if got := len(sink.Text()); got > 16 {
		t.Fatalf("retained %d bytes, want at most 16", got)
	}
	if !strings.Contains(sink.Text(), "line9") {
		t.Fatalf("the window %q should keep the newest output", sink.Text())
	}
}

func TestOutputSinkTailTrimsToLastLines(t *testing.T) {
	sink := NewOutputSink(0)
	for i := range 5 {
		if _, err := fmt.Fprintf(sink, "line%d\n", i); err != nil {
			t.Fatalf("Write(): %v", err)
		}
	}

	if got := sink.Tail(2); got != "line3\nline4" {
		t.Fatalf("Tail(2) = %q, want the last two lines", got)
	}
	if got := sink.Tail(0); !strings.Contains(got, "line0") {
		t.Fatalf("Tail(0) = %q, want the whole window", got)
	}
	if got := sink.Tail(50); !strings.Contains(got, "line0") {
		t.Fatalf("Tail() past the line count = %q, want the whole window", got)
	}
}

func TestTaskOutputAndMetadataPersistUnderTheSessionDir(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})
	sessionDir := t.TempDir()
	p.SetSessionDir("s1", sessionDir)

	snap, err := p.Start(Spec{SessionID: "s1", Command: "make build", Label: "writes"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	runner.last().finish(0)
	waitForStatus(t, p, "s1", snap.ID, StatusSucceeded)

	text, truncated, ok := PersistedOutput(sessionDir, snap.ID)
	if !ok {
		t.Fatal("PersistedOutput() found no log")
	}
	if truncated {
		t.Fatal("a small log must not report truncation")
	}
	if !strings.Contains(text, "hello from stub") {
		t.Fatalf("persisted log %q missing the command output", text)
	}

	loaded := LoadPersisted(sessionDir)
	if len(loaded) != 1 {
		t.Fatalf("LoadPersisted() returned %d rows, want 1", len(loaded))
	}
	if loaded[0].Status != StatusSucceeded || loaded[0].Command != "make build" {
		t.Fatalf("persisted snapshot = %+v", loaded[0])
	}
}

func TestLoadPersistedMarksInterruptedTasksOrphaned(t *testing.T) {
	sessionDir := t.TempDir()
	taskDir := filepath.Join(sessionDir, backgroundDirName, "bg_7")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	record := Snapshot{
		ID:        "bg_7",
		SessionID: "s1",
		Kind:      KindCommand,
		Command:   "npm run watch",
		Status:    StatusRunning,
		StartedAt: time.Now().Add(-time.Minute),
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	metaPath := filepath.Join(taskDir, metaFileName)
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	loaded := LoadPersisted(sessionDir)
	if len(loaded) != 1 || loaded[0].Status != StatusOrphaned {
		t.Fatalf("LoadPersisted() = %+v, want one orphaned row", loaded)
	}

	// The correction is derived on every read and deliberately NOT written
	// back: the HTTP list polls this, and rewriting would clobber the metadata
	// of tasks still running in this process and race the supervisor's write.
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	if strings.Contains(string(raw), string(StatusOrphaned)) {
		t.Fatalf("LoadPersisted() rewrote meta.json: %s", raw)
	}

	again := LoadPersisted(sessionDir)
	if len(again) != 1 || again[0].Status != StatusOrphaned {
		t.Fatalf("second LoadPersisted() = %+v, want one orphaned row", again)
	}
}

func TestDeriveLabel(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "explicit label wins", spec: Spec{Label: "watcher", Command: "npm run dev"}, want: "watcher"},
		{name: "command becomes the label", spec: Spec{Command: "make build"}, want: "make build"},
		{name: "only the first line is used", spec: Spec{Command: "make build\nmake test"}, want: "make build"},
		{name: "empty command falls back to the kind", spec: Spec{Kind: KindAgent}, want: "agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveLabel(tc.spec); got != tc.want {
				t.Fatalf("deriveLabel() = %q, want %q", got, tc.want)
			}
		})
	}

	long := deriveLabel(Spec{Command: strings.Repeat("x", 200)})
	if len([]rune(long)) != 60 || !strings.HasSuffix(long, "…") {
		t.Fatalf("deriveLabel() for a long command = %q, want a 60-rune elided label", long)
	}
}

func TestStatusFinished(t *testing.T) {
	for _, s := range []Status{StatusQueued, StatusRunning} {
		if s.Finished() {
			t.Fatalf("%q must not be terminal", s)
		}
	}
	for _, s := range []Status{StatusSucceeded, StatusFailed, StatusTimedOut, StatusStopped, StatusOrphaned} {
		if !s.Finished() {
			t.Fatalf("%q must be terminal", s)
		}
	}
}

func TestSetSessionDirKeepsIdsUniqueAcrossRestarts(t *testing.T) {
	sessionDir := t.TempDir()

	first := NewWithRunner(Config{}, &stubRunner{})
	first.SetSessionDir("s1", sessionDir)
	a, err := first.Start(Spec{SessionID: "s1", Command: "one"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	b, err := first.Start(Spec{SessionID: "s1", Command: "two"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	first.StopSession("s1")

	// A fresh process reads the same bundle and must not reuse those ids: the
	// on-disk log and metadata of the earlier run would be overwritten.
	second := NewWithRunner(Config{}, &stubRunner{})
	second.SetSessionDir("s1", sessionDir)
	t.Cleanup(func() { second.StopSession("s1") })
	c, err := second.Start(Spec{SessionID: "s1", Command: "three"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	if c.ID == a.ID || c.ID == b.ID {
		t.Fatalf("restarted pool reused id %q (previous run used %q and %q)", c.ID, a.ID, b.ID)
	}
	if got := highestPersistedTaskNumber(sessionDir); got < 3 {
		t.Fatalf("highestPersistedTaskNumber() = %d, want at least 3", got)
	}
}

func TestHighestPersistedTaskNumberIgnoresForeignDirectories(t *testing.T) {
	sessionDir := t.TempDir()
	root := filepath.Join(sessionDir, backgroundDirName)
	for _, name := range []string{"bg_2", "bg_11", "bg_notanumber", "scratch"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("MkdirAll(): %v", err)
		}
	}
	if got := highestPersistedTaskNumber(sessionDir); got != 11 {
		t.Fatalf("highestPersistedTaskNumber() = %d, want 11", got)
	}
	if got := highestPersistedTaskNumber(t.TempDir()); got != 0 {
		t.Fatalf("highestPersistedTaskNumber() on an empty bundle = %d, want 0", got)
	}
}

// slowStartRunner delays Start so the admission and drain races have a window
// wide enough for a test to hit deterministically.
type slowStartRunner struct {
	stub  stubRunner
	delay time.Duration
	begun chan struct{}
	once  sync.Once
}

func (r *slowStartRunner) Start(spec Spec, out io.Writer) (Handle, error) {
	r.once.Do(func() { close(r.begun) })
	time.Sleep(r.delay)
	return r.stub.Start(spec, out)
}

func TestPoolAdmissionCountsTasksThatAreStillStarting(t *testing.T) {
	// Regression: admission released the pool lock before the task was
	// registered, so two concurrent Start calls both saw room under a limit of
	// one and the session ended up running two tasks.
	runner := &slowStartRunner{delay: 150 * time.Millisecond, begun: make(chan struct{})}
	p := newTestPool(t, runner, Config{MaxConcurrent: 1})

	errs := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			_, err := p.Start(Spec{SessionID: "s1", Command: fmt.Sprintf("cmd %d", i)})
			errs <- err
		}(i)
	}

	var accepted, rejected int
	for range 2 {
		if err := <-errs; err == nil {
			accepted++
		} else if errors.Is(err, ErrPoolFull) {
			rejected++
		} else {
			t.Fatalf("Start(): unexpected error %v", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d, want exactly one of each under a limit of 1", accepted, rejected)
	}
}

func TestStopAllRefusesLaterStartsSoDrainCannotLeakAProcess(t *testing.T) {
	runner := &stubRunner{}
	p := NewWithRunner(Config{}, runner)

	if _, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	p.SetDraining(true)
	p.StopAll()

	// Drain waits for in-flight turns after stopping tasks, so a turn that
	// reaches Start afterwards must be refused rather than leaving a process
	// nothing will clean up.
	if _, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"}); !errors.Is(err, ErrDraining) {
		t.Fatalf("Start() after StopAll = %v, want ErrDraining", err)
	}
	if got := p.RunningCount("s1"); got != 0 {
		t.Fatalf("RunningCount() after drain = %d, want 0", got)
	}

	// A process that starts serving again reopens the pool.
	p.SetDraining(false)
	if _, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"}); err != nil {
		t.Fatalf("Start() after the pool reopened: %v", err)
	}
	p.StopSession("s1")
}

func TestStopDuringStartStillTerminatesTheProcess(t *testing.T) {
	// A stop that lands while the runner is still starting has no handle to act
	// on; Start must honour the claim once the handle exists.
	runner := &slowStartRunner{delay: 200 * time.Millisecond, begun: make(chan struct{})}
	p := newTestPool(t, runner, Config{})

	started := make(chan Snapshot, 1)
	go func() {
		snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"})
		if err != nil {
			t.Errorf("Start(): %v", err)
		}
		started <- snap
	}()

	<-runner.begun
	// The task is registered before the runner returns, so it is already
	// addressable by id.
	deadline := time.Now().Add(2 * time.Second)
	var taskID string
	for time.Now().Before(deadline) {
		if rows := p.List("s1"); len(rows) == 1 {
			taskID = rows[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("task was not registered while its runner was starting")
	}

	stopped, err := p.Stop("s1", taskID)
	if err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if stopped.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped", stopped.Status)
	}
	<-started
	if h := runner.stub.last(); h == nil || !h.stopped {
		t.Fatal("the started handle was never stopped")
	}
}

func TestNaturalExitIsNotRelabelledByALateStop(t *testing.T) {
	// Regression: Stop checked only "not finished yet", so a process that had
	// already exited successfully could be recorded as stopped.
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "true"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	runner.last().finish(0)
	waitForStatus(t, p, "s1", snap.ID, StatusSucceeded)

	after, err := p.Stop("s1", snap.ID)
	if err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if after.Status != StatusSucceeded {
		t.Fatalf("status = %q after stopping a finished task, want succeeded", after.Status)
	}
}

func TestConcurrentStopSettlesOnOneOutcome(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	results := make(chan Snapshot, 4)
	for range 4 {
		go func() {
			got, stopErr := p.Stop("s1", snap.ID)
			if stopErr != nil {
				t.Errorf("Stop(): %v", stopErr)
			}
			results <- got
		}()
	}
	for range 4 {
		got := <-results
		if got.Status != StatusStopped {
			t.Fatalf("status = %q, want stopped from every concurrent caller", got.Status)
		}
	}
}

func TestWaitReturnsWhenTheCallerIsCancelled(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, waitErr := p.Wait(ctx, "s1", snap.ID, time.Hour)
		done <- waitErr
	}()

	cancel()
	select {
	case waitErr := <-done:
		if !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("Wait() = %v, want context.Canceled", waitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() stayed parked after its caller was cancelled")
	}
}

func TestPersistedOutputReturnsABoundedTail(t *testing.T) {
	sessionDir := t.TempDir()
	taskDir := filepath.Join(sessionDir, backgroundDirName, "bg_1")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	// Larger than the cap, so the reader must not pull the whole file in.
	body := strings.Repeat("a", maxPersistedTailBytes) + "TAIL-MARKER"
	if err := os.WriteFile(filepath.Join(taskDir, outputFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	text, truncated, ok := PersistedOutput(sessionDir, "bg_1")
	if !ok {
		t.Fatal("PersistedOutput() found no log")
	}
	if !truncated {
		t.Fatal("a log past the cap must report truncation")
	}
	if len(text) > maxPersistedTailBytes {
		t.Fatalf("returned %d bytes, want at most %d", len(text), maxPersistedTailBytes)
	}
	if !strings.HasSuffix(text, "TAIL-MARKER") {
		t.Fatal("the returned slice is not the tail of the log")
	}

	if _, _, ok := PersistedOutput(sessionDir, "bg_missing"); ok {
		t.Fatal("PersistedOutput() invented a log for an unknown task")
	}
}

// startDetachedHelper launches a process that leads its own group, standing in
// for what an earlier coddy run left behind, and kills it when the test ends. It
// returns the leader pid and the moment it was started: the survivor probe
// compares that against the record's StartedAt to tell the process apart from a
// stranger that inherited its pid.
//
// It goes through CommandRunner rather than exec.Command directly so the helper
// is started exactly the way a real task is - host shell, own process group -
// and so these tests run on Windows instead of skipping for want of `sleep`.
func startDetachedHelper(t *testing.T) (int, time.Time) {
	t.Helper()

	started := time.Now()
	handle, err := NewCommandRunner().Start(Spec{Command: helperSleepCommand(t)}, io.Discard)
	if err != nil {
		t.Skipf("cannot start a helper process: %v", err)
	}
	pid := handle.PID()
	go func() { _, _ = handle.Wait() }()
	t.Cleanup(func() { _ = platform.TerminateProcessGroupByPID(pid, time.Second) })
	return pid, started
}

// helperSleepCommand keeps the helper shell-neutral, the same way the background
// task feature steps do.
func helperSleepCommand(t *testing.T) string {
	t.Helper()
	switch kind := platform.CurrentShell().Kind; kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "Start-Sleep -Seconds 600"
	case platform.ShellBash, platform.ShellSh:
		return "sleep 600"
	default:
		t.Skipf("no portable sleep for shell %q", kind)
		return ""
	}
}

func TestSnapshotSilentFor(t *testing.T) {
	start := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	wrote := start.Add(10 * time.Second)

	running := Snapshot{Status: StatusRunning, StartedAt: start, LastOutputAt: &wrote}
	if got := running.SilentFor(wrote.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("SilentFor() = %v, want 90s", got)
	}

	// A task that never wrote has no silence to measure against.
	never := Snapshot{Status: StatusRunning, StartedAt: start}
	if got := never.SilentFor(start.Add(time.Hour)); got != 0 {
		t.Fatalf("SilentFor() without output = %v, want 0", got)
	}

	// Neither does a finished one: silence only matters while work is alleged
	// to be happening.
	done := Snapshot{Status: StatusSucceeded, StartedAt: start, LastOutputAt: &wrote}
	if got := done.SilentFor(wrote.Add(time.Hour)); got != 0 {
		t.Fatalf("SilentFor() after finish = %v, want 0", got)
	}
}

func TestRunningTaskRecordsWhenItLastWrote(t *testing.T) {
	runner := &stubRunner{}
	p := newTestPool(t, runner, Config{})

	snap, err := p.Start(Spec{SessionID: "s1", Command: "make", Label: "writes"})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	got, err := p.Get("s1", snap.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.LastOutputAt == nil {
		t.Fatal("a task that wrote output must record when it last did")
	}
}

func TestSurvivorsAreOnlyRecordsWithALiveProcess(t *testing.T) {
	sessionDir := t.TempDir()
	p := NewWithRunner(Config{}, &stubRunner{})
	p.SetSessionDir("s1", sessionDir)

	// The probe asks about a process GROUP, and tasks are started with Setpgid
	// so the child leads its own group. The test therefore needs a real group
	// leader, not this process, which usually is not one. Its start time has to
	// be truthful too: the Windows probe rejects a pid whose process was created
	// at a different time from the one the record claims.
	leader, startedAt := startDetachedHelper(t)

	write := func(id string, pid int, status Status) {
		dir := filepath.Join(sessionDir, backgroundDirName, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(): %v", err)
		}
		data, err := json.Marshal(Snapshot{
			ID: id, SessionID: "s1", Kind: KindCommand, Label: id,
			Status: status, StartedAt: startedAt, PID: pid,
		})
		if err != nil {
			t.Fatalf("Marshal(): %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0o644); err != nil {
			t.Fatalf("WriteFile(): %v", err)
		}
	}

	write("bg_alive", leader, StatusRunning)
	write("bg_nopid", 0, StatusRunning)
	write("bg_finished", leader, StatusSucceeded)

	got := p.Survivors("s1")
	if len(got) != 1 || got[0].ID != "bg_alive" {
		ids := make([]string, 0, len(got))
		for _, s := range got {
			ids = append(ids, s.ID)
		}
		t.Fatalf("Survivors() = %v, want only bg_alive", ids)
	}
}

func TestSurvivorsIgnoreTasksThisPoolAlreadyOwns(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &stubRunner{}
	p := NewWithRunner(Config{}, runner)
	p.SetSessionDir("s1", sessionDir)
	t.Cleanup(func() { p.StopSession("s1") })

	if _, err := p.Start(Spec{SessionID: "s1", Command: "sleep 600"}); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	// A task the pool is supervising is not a survivor, even though its record
	// is on disk: background_stop already reaches it.
	if got := p.Survivors("s1"); len(got) != 0 {
		t.Fatalf("Survivors() = %d, want none while the pool owns the task", len(got))
	}
}

func TestStopReachesASurvivorByItsRecordedGroup(t *testing.T) {
	sessionDir := t.TempDir()
	p := NewWithRunner(Config{}, &stubRunner{})
	p.SetSessionDir("s1", sessionDir)

	// A real process this pool never started, in its own group, standing in for
	// what an earlier coddy left behind.
	pid, startedAt := startDetachedHelper(t)

	dir := filepath.Join(sessionDir, backgroundDirName, "bg_left")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	data, _ := json.Marshal(Snapshot{
		ID: "bg_left", SessionID: "s1", Kind: KindCommand, Label: "sleep 600",
		Status: StatusRunning, StartedAt: startedAt, PID: pid,
	})
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	got, err := p.Stop("s1", "bg_left")
	if err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if got.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped", got.Status)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !platform.ProcessGroupAlive(pid, startedAt) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the survivor process group is still alive after Stop")
}

func TestReapSurvivorsKillsAndRecordsThem(t *testing.T) {
	sessionDir := t.TempDir()
	p := NewWithRunner(Config{}, &stubRunner{})
	p.SetSessionDir("s1", sessionDir)

	pid, startedAt := startDetachedHelper(t)

	dir := filepath.Join(sessionDir, backgroundDirName, "bg_left")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	data, _ := json.Marshal(Snapshot{
		ID: "bg_left", SessionID: "s1", Kind: KindCommand, Label: "sleep 600",
		Status: StatusRunning, StartedAt: startedAt, PID: pid,
	})
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	reaped := p.ReapSurvivors("s1")
	if len(reaped) != 1 || reaped[0].ID != "bg_left" {
		t.Fatalf("ReapSurvivors() = %+v, want one row", reaped)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && platform.ProcessGroupAlive(pid, startedAt) {
		time.Sleep(20 * time.Millisecond)
	}
	if platform.ProcessGroupAlive(pid, startedAt) {
		t.Fatal("the reaped process group is still alive")
	}

	// The record is rewritten, so a second call does not offer the same kill.
	if got := p.Survivors("s1"); len(got) != 0 {
		t.Fatalf("Survivors() after reaping = %+v, want none", got)
	}
	if got := p.ReapSurvivors("s1"); len(got) != 0 {
		t.Fatalf("ReapSurvivors() twice = %+v, want none the second time", got)
	}
}

func TestSurvivorsRefuseFinishedRecordsBecausePidsGetReused(t *testing.T) {
	sessionDir := t.TempDir()
	p := NewWithRunner(Config{}, &stubRunner{})
	p.SetSessionDir("s1", sessionDir)

	// A task that finished normally leaves a stale pid behind. The OS reuses
	// pids, so that number may now belong to something else entirely; offering
	// it for killing would hand the agent a stranger's process.
	leader, startedAt := startDetachedHelper(t)
	dir := filepath.Join(sessionDir, backgroundDirName, "bg_done")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	data, _ := json.Marshal(Snapshot{
		ID: "bg_done", SessionID: "s1", Kind: KindCommand, Label: "echo hi",
		Status: StatusSucceeded, StartedAt: startedAt, PID: leader,
	})
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if got := p.Survivors("s1"); len(got) != 0 {
		t.Fatalf("Survivors() = %+v, want none for a finished record", got)
	}
	if got := p.ReapSurvivors("s1"); len(got) != 0 {
		t.Fatalf("ReapSurvivors() = %+v, want none for a finished record", got)
	}
	if !platform.ProcessGroupAlive(leader, startedAt) {
		t.Fatal("a finished record must not get an unrelated process killed")
	}
}
