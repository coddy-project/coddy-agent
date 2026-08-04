package bgtask

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Defaults used when the configuration leaves a knob unset.
const (
	defaultMaxConcurrent      = 5
	defaultTimeoutSeconds     = 900
	defaultMaxTimeoutSeconds  = 3600
	defaultOutputBufferBytes  = 256 * 1024
	defaultStopGrace          = 3 * time.Second
	timeoutEstimateMultiplier = 3
	// minEstimatedTimeoutSeconds keeps an estimate-derived timeout from being so
	// tight that a slightly slow command is killed for no good reason.
	minEstimatedTimeoutSeconds = 60
)

// ErrPoolFull is returned when a session already runs its maximum number of
// concurrent tasks.
var ErrPoolFull = errors.New("background task pool is full for this session")

// ErrNotFound is returned when a task id is unknown to the pool.
var ErrNotFound = errors.New("background task not found")

// ErrDraining is returned once the process is shutting down, so a turn racing
// drain cannot start work nothing will clean up.
var ErrDraining = errors.New("background task pool is shutting down")

// Config bounds what the pool will accept.
type Config struct {
	// MaxConcurrent is how many tasks one session may run at once.
	MaxConcurrent int
	// DefaultTimeoutSeconds applies when neither the caller nor the estimate
	// implies a limit.
	DefaultTimeoutSeconds int
	// MaxTimeoutSeconds caps whatever the caller or the estimate asked for.
	MaxTimeoutSeconds int
	// OutputBufferBytes is the in-memory output window per task.
	OutputBufferBytes int
}

func (c Config) normalised() Config {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = defaultMaxConcurrent
	}
	if c.DefaultTimeoutSeconds <= 0 {
		c.DefaultTimeoutSeconds = defaultTimeoutSeconds
	}
	if c.MaxTimeoutSeconds <= 0 {
		c.MaxTimeoutSeconds = defaultMaxTimeoutSeconds
	}
	if c.DefaultTimeoutSeconds > c.MaxTimeoutSeconds {
		c.DefaultTimeoutSeconds = c.MaxTimeoutSeconds
	}
	if c.OutputBufferBytes <= 0 {
		c.OutputBufferBytes = defaultOutputBufferBytes
	}
	return c
}

// Pool owns every background task of every live session in this process.
type Pool struct {
	mu       sync.RWMutex
	cfg      Config
	runner   Runner
	tasks    map[string]*task
	order    []string
	seq      int
	draining bool
	now      func() time.Time
	watchers []func(Snapshot)

	// keyedWatchers are subscriptions that replace rather than accumulate. A
	// process that builds more than one server (tests do, every scenario) would
	// otherwise stack a watcher per server and wake the agent once per stacked
	// copy.
	keyedWatchers map[string]func(Snapshot)

	// sessionDirs maps a session id to its persisted bundle, so a task started
	// from a tool call knows where to mirror its output without the caller
	// threading the path through every call.
	sessionDirs map[string]string
}

// New returns a pool that runs shell commands on the host interpreter.
func New(cfg Config) *Pool {
	return NewWithRunner(cfg, NewCommandRunner())
}

// NewWithRunner returns a pool driven by a custom runner. Tests use this to stay
// off the host shell; a future nested-agent runner plugs in the same way.
func NewWithRunner(cfg Config, runner Runner) *Pool {
	if runner == nil {
		runner = NewCommandRunner()
	}
	return &Pool{
		cfg:           cfg.normalised(),
		runner:        runner,
		tasks:         map[string]*task{},
		now:           time.Now,
		sessionDirs:   map[string]string{},
		keyedWatchers: map[string]func(Snapshot){},
	}
}

// SetSessionDir records where a session persists its bundle. Tasks started for
// that session mirror output and metadata under <dir>/background/<taskID>/.
//
// It also advances the id counter past anything the bundle already records. A
// restart otherwise starts numbering at bg_1 again and the new task overwrites
// the log and metadata of the identically named task from the previous process.
func (p *Pool) SetSessionDir(sessionID, dir string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	dir = strings.TrimSpace(dir)

	highest := 0
	if dir != "" {
		highest = highestPersistedTaskNumber(dir)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if dir == "" {
		delete(p.sessionDirs, sessionID)
		return
	}
	p.sessionDirs[sessionID] = dir
	p.seq = max(p.seq, highest)
}

// Subscribe registers a callback invoked with every snapshot change. Callbacks
// run on the pool's own goroutines, so they must not block.
func (p *Pool) Subscribe(fn func(Snapshot)) {
	if fn == nil {
		return
	}
	p.mu.Lock()
	p.watchers = append(p.watchers, fn)
	p.mu.Unlock()
}

// SubscribeKeyed registers a watcher under a key, replacing any previous
// watcher with that key. Use it for a subscription owned by a component that
// can be rebuilt within one process; a nil fn removes the subscription.
func (p *Pool) SubscribeKeyed(key string, fn func(Snapshot)) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if fn == nil {
		delete(p.keyedWatchers, key)
		return
	}
	p.keyedWatchers[key] = fn
}

// Draining reports whether the pool is closed to new work.
func (p *Pool) Draining() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.draining
}

// task is the pool's private bookkeeping for one unit of work.
type task struct {
	mu   sync.Mutex
	snap Snapshot
	sink *OutputSink
	dir  string

	handle Handle
	done   chan struct{}

	// termReason records why the task is being terminated. Exactly one caller
	// wins the claim, so a stop that lands microseconds before the timeout is
	// still reported as a stop rather than as a timeout.
	termReason Status

	// exitObserved is set the moment the process is reaped, which makes a
	// termination claim that arrives afterwards lose: work that already
	// succeeded must not be relabelled as stopped.
	exitObserved bool
}

// claimTermination records the reason a task is about to be terminated and
// returns the handle to act on. Only the winning caller gets ok, so the handle
// is never stopped twice and the recorded reason never flips.
func (t *task) claimTermination(reason Status) (Handle, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exitObserved || t.snap.Status.Finished() || t.termReason != "" {
		return nil, false
	}
	t.termReason = reason
	return t.handle, true
}

// attachHandle publishes the started handle and reports whether termination was
// already claimed while the runner was starting. A task can be stopped in that
// window (drain, an impatient operator), and the claim would otherwise have had
// no handle to act on.
func (t *task) attachHandle(h Handle) (terminateNow bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handle = h
	if h != nil {
		t.snap.PID = h.PID()
		t.snap.ProcessStartedAt = h.ProcessStartedAt()
	}
	return t.termReason != ""
}

// markExitObserved is called as soon as the process is reaped.
func (t *task) markExitObserved() {
	t.mu.Lock()
	t.exitObserved = true
	t.mu.Unlock()
}

// AdoptFunc hands the pool's output sink to work that is already running and
// returns the handle controlling it. It is the same shape as Runner.Start minus
// the Spec, which adopted work cannot act on: the process exists already.
type AdoptFunc func(out io.Writer) (Handle, error)

// Start accepts a spec and launches it. It returns as soon as the work is
// running: the caller gets a task id, not a result.
func (p *Pool) Start(spec Spec) (Snapshot, error) {
	return p.start(spec, func(out io.Writer) (Handle, error) { return p.runner.Start(spec, out) })
}

// Adopt registers work that is already running - a foreground command that
// outlived its timeout and must not be killed for it. The callback receives the
// task's output sink, so everything the work has written so far can be flushed
// into it and the log reads as one stream.
//
// Admission happens before the callback runs, so ErrPoolFull and ErrDraining
// guarantee it was never called and the caller still owns the process.
//
// A zero TimeoutSeconds becomes the configured maximum rather than the ordinary
// default: adopted work has no estimate behind it, and the foreground limit it
// just outlived is the one value that must never be reused - resolveTimeoutSeconds
// honours an explicit timeout verbatim, so passing the expired one would kill the
// task immediately. NotifyOnFinish is forced off because the caller is being told
// about this task right now, in the tool result.
func (p *Pool) Adopt(spec Spec, adopt AdoptFunc) (Snapshot, error) {
	if adopt == nil {
		return Snapshot{}, fmt.Errorf("adopt callback is nil")
	}
	if spec.TimeoutSeconds <= 0 {
		p.mu.RLock()
		spec.TimeoutSeconds = p.cfg.MaxTimeoutSeconds
		p.mu.RUnlock()
	}
	spec.NotifyOnFinish = false
	return p.start(spec, adopt)
}

// start is the one scheduling path: Start launches through the runner, Adopt
// hands over work that is already running, and everything after the launch -
// admission, persistence, supervision - is identical.
func (p *Pool) start(spec Spec, launch AdoptFunc) (Snapshot, error) {
	spec.SessionID = strings.TrimSpace(spec.SessionID)
	if spec.Kind == "" {
		spec.Kind = KindCommand
	}
	if spec.Kind == KindCommand && strings.TrimSpace(spec.Command) == "" {
		return Snapshot{}, fmt.Errorf("background command is empty")
	}

	p.mu.Lock()
	if p.draining {
		p.mu.Unlock()
		return Snapshot{}, ErrDraining
	}
	if p.runningForSession(spec.SessionID) >= p.cfg.MaxConcurrent {
		p.mu.Unlock()
		return Snapshot{}, fmt.Errorf("%w (limit %d)", ErrPoolFull, p.cfg.MaxConcurrent)
	}
	p.seq++
	id := fmt.Sprintf("%s%d", taskIDPrefix, p.seq)
	sessionDir := p.sessionDirs[spec.SessionID]
	cfg := p.cfg
	now := p.now()

	timeoutSeconds := resolveTimeoutSeconds(spec, cfg)

	startedAt := now
	if !spec.StartedAt.IsZero() {
		startedAt = spec.StartedAt
	}

	t := &task{
		sink: NewOutputSink(cfg.OutputBufferBytes),
		done: make(chan struct{}),
		snap: Snapshot{
			ID:              id,
			SessionID:       spec.SessionID,
			Kind:            spec.Kind,
			Label:           deriveLabel(spec),
			Command:         spec.Command,
			CWD:             spec.CWD,
			ToolCallID:      spec.ToolCallID,
			Status:          StatusRunning,
			StartedAt:       startedAt,
			ExpectedSeconds: spec.ExpectedSeconds,
			TimeoutSeconds:  timeoutSeconds,
			NotifyOnFinish:  spec.NotifyOnFinish,
		},
	}

	// Register before releasing the lock. Starting the runner can be slow, and
	// admission must count this task meanwhile: otherwise two concurrent Start
	// calls both see room under the limit, and a drain that snapshots the task
	// map in between never learns the process exists.
	p.registerLocked(t)
	p.mu.Unlock()

	if sessionDir != "" {
		t.dir = filepath.Join(sessionDir, backgroundDirName, id)
		if err := prepareTaskDir(t.dir); err == nil {
			_ = t.sink.AttachFile(filepath.Join(t.dir, outputFileName))
		}
	}

	handle, err := launch(t.sink)
	if err != nil {
		t.sink.Close()
		finished := p.now()
		t.mu.Lock()
		t.snap.Status = StatusFailed
		t.snap.Error = err.Error()
		t.snap.FinishedAt = &finished
		t.mu.Unlock()
		close(t.done)
		p.persist(t)
		p.notify(t.Snapshot(p.now()))
		return t.Snapshot(p.now()), err
	}

	if terminateNow := t.attachHandle(handle); terminateNow {
		_ = handle.Stop(defaultStopGrace)
	}
	p.persist(t)

	snap := t.Snapshot(now)
	p.notify(snap)

	go p.supervise(t, time.Duration(timeoutSeconds)*time.Second)

	return snap, nil
}

// resolveTimeoutSeconds turns the caller's request and the model's estimate into
// a hard limit.
//
// An explicit timeout always wins, including a deliberately tight one. Otherwise
// a stated estimate buys a multiple of itself, floored so that a wildly
// optimistic guess does not kill work that was only a little slow, and the
// result is capped by the configured ceiling. With no estimate at all the
// configured default applies.
func resolveTimeoutSeconds(spec Spec, cfg Config) int {
	seconds := spec.TimeoutSeconds
	switch {
	case seconds > 0:
	case spec.ExpectedSeconds > 0:
		seconds = max(spec.ExpectedSeconds*timeoutEstimateMultiplier, minEstimatedTimeoutSeconds)
	default:
		seconds = cfg.DefaultTimeoutSeconds
	}
	return min(seconds, cfg.MaxTimeoutSeconds)
}

// registerLocked must be called with the pool lock held.
func (p *Pool) registerLocked(t *task) {
	key := taskKey(t.snap.SessionID, t.snap.ID)
	p.tasks[key] = t
	p.order = append(p.order, key)
}

// supervise waits for the task to exit, terminating it when the hard timeout
// elapses first.
func (p *Pool) supervise(t *task, timeout time.Duration) {
	type exitResult struct {
		code int
		err  error
	}
	exited := make(chan exitResult, 1)
	go func() {
		code, err := t.handle.Wait()
		// Reaped: from here on a termination claim must lose, so work that
		// already finished is never relabelled as stopped or timed out.
		t.markExitObserved()
		exited <- exitResult{code: code, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var result exitResult
	select {
	case result = <-exited:
	case <-timer.C:
		if handle, ok := t.claimTermination(StatusTimedOut); ok && handle != nil {
			_ = handle.Stop(defaultStopGrace)
		}
		result = <-exited
	}

	t.sink.Close()
	finished := p.now()

	t.mu.Lock()
	switch {
	case t.termReason != "":
		// The process was killed on purpose; its exit error is expected and
		// says nothing useful.
		t.snap.Status = t.termReason
	case result.err != nil:
		t.snap.Status = StatusFailed
		t.snap.Error = result.err.Error()
	case result.code == 0:
		t.snap.Status = StatusSucceeded
	default:
		t.snap.Status = StatusFailed
	}
	code := result.code
	t.snap.ExitCode = &code
	t.snap.FinishedAt = &finished
	t.mu.Unlock()

	// Persist before releasing waiters: Stop and Wait return the moment done
	// closes, and a caller that then reads the bundle must not find a record
	// that still says the task is running.
	p.persist(t)
	close(t.done)
	p.notify(t.Snapshot(finished))
}

// Snapshot returns the current view of the task, refreshed with output counters.
func (t *task) Snapshot(_ time.Time) Snapshot {
	total, truncated := t.sink.Stats()
	last := t.sink.LastWrite()
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.snap
	snap.OutputBytes = total
	snap.OutputTruncated = truncated
	if !last.IsZero() {
		when := last
		snap.LastOutputAt = &when
	}
	return snap
}

// List returns every task of a session, oldest first.
func (p *Pool) List(sessionID string) []Snapshot {
	sessionID = strings.TrimSpace(sessionID)
	now := p.now()

	p.mu.RLock()
	keys := append([]string(nil), p.order...)
	tasks := make([]*task, 0, len(keys))
	for _, key := range keys {
		if t, ok := p.tasks[key]; ok && t.snap.SessionID == sessionID {
			tasks = append(tasks, t)
		}
	}
	p.mu.RUnlock()

	out := make([]Snapshot, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Snapshot(now))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Get returns one task snapshot.
func (p *Pool) Get(sessionID, taskID string) (Snapshot, error) {
	t, err := p.lookup(sessionID, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	return t.Snapshot(p.now()), nil
}

// Output returns the retained output window for a task. A positive tailLines
// trims the result to that many trailing lines.
func (p *Pool) Output(sessionID, taskID string, tailLines int) (string, Snapshot, error) {
	t, err := p.lookup(sessionID, taskID)
	if err != nil {
		return "", Snapshot{}, err
	}
	return t.sink.Tail(tailLines), t.Snapshot(p.now()), nil
}

// Stop terminates a running task and waits for it to settle. Stopping a task
// that already finished, or that another caller is already terminating, is a
// no-op that still returns the settled snapshot.
//
// A task this process never started - a survivor of an earlier coddy, recorded
// in the session bundle - is reached by its recorded process group instead.
func (p *Pool) Stop(sessionID, taskID string) (Snapshot, error) {
	t, err := p.lookup(sessionID, taskID)
	if err != nil {
		if survivor, ok := p.stopSurvivor(sessionID, taskID); ok {
			return survivor, nil
		}
		return Snapshot{}, err
	}

	handle, claimed := t.claimTermination(StatusStopped)
	if claimed && handle != nil {
		if err := handle.Stop(defaultStopGrace); err != nil {
			return t.Snapshot(p.now()), err
		}
	}

	// A task reserved but not yet started has no handle; Start honours the
	// claim as soon as the runner hands one over, so the wait still resolves.
	<-t.done
	return t.Snapshot(p.now()), nil
}

// Wait blocks until the task finishes, the timeout elapses, or ctx is done. A
// timeout is not an error: the caller gets the still-running snapshot back and
// can poll again. Taking a context keeps a cancelled caller from leaving this
// call parked for the rest of its timeout.
func (p *Pool) Wait(ctx context.Context, sessionID, taskID string, timeout time.Duration) (Snapshot, error) {
	t, err := p.lookup(sessionID, taskID)
	if err != nil {
		return Snapshot{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		select {
		case <-t.done:
		case <-ctx.Done():
			return t.Snapshot(p.now()), ctx.Err()
		}
		return t.Snapshot(p.now()), nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
	case <-ctx.Done():
		return t.Snapshot(p.now()), ctx.Err()
	}
	return t.Snapshot(p.now()), nil
}

// stopSurvivor terminates a task left behind by an earlier coddy process, using
// the process group id the bundle recorded. Reports false when the bundle has no
// such task.
func (p *Pool) stopSurvivor(sessionID, taskID string) (Snapshot, bool) {
	p.mu.RLock()
	dir := p.sessionDirs[strings.TrimSpace(sessionID)]
	p.mu.RUnlock()
	if dir == "" {
		return Snapshot{}, false
	}

	for _, snap := range LoadPersisted(dir) {
		if snap.ID != strings.TrimSpace(taskID) {
			continue
		}
		// Same reasoning as Survivors: a finished record's pid is stale, and
		// pids get reused.
		if snap.Status == StatusOrphaned && snap.PID > 0 && platform.ProcessGroupAlive(snap.PID, snap.ProcessStartedAt) {
			_ = platform.TerminateProcessGroupByPID(snap.PID, snap.ProcessStartedAt, defaultStopGrace)
			snap.Status = StatusStopped
			finished := p.now()
			snap.FinishedAt = &finished
			writePersistedSnapshot(dir, snap)
		}
		return snap, true
	}
	return Snapshot{}, false
}

// Survivors returns the tasks of a session whose processes outlived the coddy
// run that started them and are still on this machine. They are what an agent
// reaps after a crash, and the only tasks the pool itself cannot stop.
func (p *Pool) Survivors(sessionID string) []Snapshot {
	p.mu.RLock()
	dir := p.sessionDirs[strings.TrimSpace(sessionID)]
	p.mu.RUnlock()
	if dir == "" {
		return nil
	}

	live := map[string]bool{}
	for _, snap := range p.List(sessionID) {
		live[snap.ID] = true
	}

	var out []Snapshot
	for _, snap := range LoadPersisted(dir) {
		if live[snap.ID] || snap.PID <= 0 {
			continue
		}
		// Only a record that still claimed to be running when its process died
		// is a survivor. A task that finished normally has a stale pid, and
		// pids get reused: acting on one would kill an unrelated process. The
		// recorded process creation time is what lets the probe reject a pid that
		// has since been handed to something else.
		if snap.Status != StatusOrphaned {
			continue
		}
		if platform.ProcessGroupAlive(snap.PID, snap.ProcessStartedAt) {
			out = append(out, snap)
		}
	}
	return out
}

// ReapSurvivors terminates every survivor of a session and returns what it
// killed.
func (p *Pool) ReapSurvivors(sessionID string) []Snapshot {
	p.mu.RLock()
	dir := p.sessionDirs[strings.TrimSpace(sessionID)]
	p.mu.RUnlock()

	reaped := p.Survivors(sessionID)
	for i := range reaped {
		_ = platform.TerminateProcessGroupByPID(reaped[i].PID, reaped[i].ProcessStartedAt, defaultStopGrace)
		reaped[i].Status = StatusStopped
		finished := p.now()
		reaped[i].FinishedAt = &finished
		if dir != "" {
			writePersistedSnapshot(dir, reaped[i])
		}
	}
	return reaped
}

// StopSession terminates every running task of a session. It is what session
// deletion and server drain call.
func (p *Pool) StopSession(sessionID string) {
	for _, snap := range p.List(sessionID) {
		if snap.Status.Finished() {
			continue
		}
		_, _ = p.Stop(sessionID, snap.ID)
	}
}

// SetDraining opens or closes the pool to new work. Server shutdown closes it
// before stopping tasks, so a turn that is still winding down cannot start work
// nothing will clean up; constructing a server opens it again, because a
// process that is serving again is a process that will look after its children.
func (p *Pool) SetDraining(draining bool) {
	p.mu.Lock()
	p.draining = draining
	p.mu.Unlock()
}

// StopAll terminates every running task in the process. Pair it with
// SetDraining(true) beforehand when the process is going away, so nothing new
// starts behind it.
func (p *Pool) StopAll() {
	p.mu.Lock()
	sessions := make(map[string]bool, len(p.tasks))
	for _, t := range p.tasks {
		sessions[t.snap.SessionID] = true
	}
	p.mu.Unlock()

	for sessionID := range sessions {
		p.StopSession(sessionID)
	}
}

// ClearFinished drops every terminal task of a session, in memory and on disk,
// and reports how many went. Running tasks are left alone. History accumulates
// on its own, so this is the operator's explicit way to throw it away rather
// than an automatic retention sweep.
func (p *Pool) ClearFinished(sessionID string) int {
	sessionID = strings.TrimSpace(sessionID)

	p.mu.Lock()
	sessionDir := p.sessionDirs[sessionID]
	kept := make([]string, 0, len(p.order))
	removed := make([]*task, 0)
	for _, key := range p.order {
		t, ok := p.tasks[key]
		if !ok {
			continue
		}
		t.mu.Lock()
		finished := t.snap.Status.Finished()
		t.mu.Unlock()
		if t.snap.SessionID == sessionID && finished {
			delete(p.tasks, key)
			removed = append(removed, t)
			continue
		}
		kept = append(kept, key)
	}
	p.order = kept
	p.mu.Unlock()

	for _, t := range removed {
		if t.dir != "" {
			_ = os.RemoveAll(t.dir)
		}
	}

	// Records left behind by an earlier process are part of the same history,
	// so clearing has to reach them too or they reappear on the next poll.
	cleared := len(removed)
	if sessionDir != "" {
		live := map[string]bool{}
		for _, snap := range p.List(sessionID) {
			live[snap.ID] = true
		}
		for _, snap := range LoadPersisted(sessionDir) {
			if live[snap.ID] || !snap.Status.Finished() {
				continue
			}
			if err := os.RemoveAll(filepath.Join(sessionDir, backgroundDirName, snap.ID)); err == nil {
				cleared++
			}
		}
	}
	return cleared
}

// RunningCount reports how many tasks of a session are still in flight.
func (p *Pool) RunningCount(sessionID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runningForSession(strings.TrimSpace(sessionID))
}

// runningForSession must be called with the pool lock held.
func (p *Pool) runningForSession(sessionID string) int {
	count := 0
	for _, t := range p.tasks {
		if t.snap.SessionID != sessionID {
			continue
		}
		t.mu.Lock()
		running := !t.snap.Status.Finished()
		t.mu.Unlock()
		if running {
			count++
		}
	}
	return count
}

func (p *Pool) lookup(sessionID, taskID string) (*task, error) {
	key := taskKey(strings.TrimSpace(sessionID), strings.TrimSpace(taskID))
	p.mu.RLock()
	t, ok := p.tasks[key]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, taskID)
	}
	return t, nil
}

func (p *Pool) notify(snap Snapshot) {
	p.mu.RLock()
	watchers := make([]func(Snapshot), 0, len(p.watchers)+len(p.keyedWatchers))
	watchers = append(watchers, p.watchers...)
	for _, fn := range p.keyedWatchers {
		watchers = append(watchers, fn)
	}
	p.mu.RUnlock()
	for _, fn := range watchers {
		fn(snap)
	}
}

func taskKey(sessionID, taskID string) string {
	return sessionID + "\x00" + taskID
}

// decodeOutput reuses the platform decoder so Windows console output arrives as
// UTF-8 exactly like it does for a foreground run_command.
func decodeOutput(b []byte) string {
	return platform.SanitizeOutput(platform.DecodeOutput(b))
}
