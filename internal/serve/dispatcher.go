package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ExitRestart is the status a worker exits with when it wants a replacement
// rather than to stop: a configuration change moved something the running
// process cannot adopt, such as the address its listener is bound to.
//
// It is sysexits' EX_TEMPFAIL, which is what a supervisor that does not know
// about Coddy - systemd with RestartForceExitStatus, an operator's own wrapper -
// would read it as anyway: this run failed, another one is worth starting.
const ExitRestart = 75

// Restart pacing. A surface that cannot start - a port somebody else holds, a
// database that is not up yet - must not be retried in a tight loop, and a
// daemon that has been up for a while must not inherit the pace of a bad hour
// last week.
const (
	defaultRestartMin    = time.Second
	defaultRestartMax    = 30 * time.Second
	defaultRestartSteady = time.Minute
)

// RestartPolicy is how long the dispatcher waits before starting a worker that
// failed, and what it takes for that wait to go back to the beginning.
type RestartPolicy struct {
	// Min is the first wait after a failure, and the wait a healthy worker
	// resets to.
	Min time.Duration
	// Max caps the doubling. A daemon whose dependency is down for a day should
	// come back within seconds of it returning, not within an hour.
	Max time.Duration
	// Steady is how long a worker has to last for its failure to count as a
	// fresh problem rather than a continuation of the last one.
	Steady time.Duration
}

func (p RestartPolicy) normalized() RestartPolicy {
	if p.Min <= 0 {
		p.Min = defaultRestartMin
	}
	if p.Max < p.Min {
		p.Max = defaultRestartMax
	}
	if p.Max < p.Min {
		p.Max = p.Min
	}
	if p.Steady <= 0 {
		p.Steady = defaultRestartSteady
	}
	return p
}

// next doubles the previous wait up to Max, starting at Min.
func (p RestartPolicy) next(prev time.Duration) time.Duration {
	if prev <= 0 {
		return p.Min
	}
	next := prev * 2
	if next > p.Max {
		return p.Max
	}
	return next
}

// Dispatcher keeps one worker running.
//
// It is deliberately ignorant of what the worker is. `coddy serve --daemon`
// hands it a function that starts this same binary again with the subsystems in
// it, which is what makes a crash - a panic that escaped, an OOM kill, a
// listener that died with its network - survivable: the surfaces come back in a
// process built from the configuration on disk, rather than being restarted in
// place inside one whose state nobody can vouch for any more.
//
// It never gives up. A worker that fails on every attempt is waited out with a
// growing pause, capped, forever. Something an operator has to notice and fix
// will be there when they look; something transient will have passed.
type Dispatcher struct {
	// Worker starts one worker and blocks until it exits, reporting its exit
	// status. A status it cannot determine is reported as a non-zero one, since
	// a worker that ended in a way nobody understood is not one to stop over.
	Worker func(ctx context.Context) (int, error)
	// Policy paces the restarts. The zero value is the default pacing.
	Policy RestartPolicy
	// Log records every worker life.
	Log *slog.Logger
	// Wait pauses between attempts and reports whether the pause ran to its end
	// rather than being cut short by ctx. Nil uses a real timer; a test uses its
	// own so the pacing can be asserted without living through it.
	Wait func(ctx context.Context, d time.Duration) bool
	// Now reads the clock the uptime is measured against. Nil uses time.Now.
	Now func() time.Time
}

// Run supervises workers until ctx ends or a worker exits cleanly.
//
// A clean exit is an instruction, not an outcome to recover from: it is what a
// worker does when the operator asked the daemon to stop, so bringing another
// one up would be arguing with them.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d.Worker == nil {
		return errors.New("dispatcher has no worker to run")
	}
	policy := d.Policy.normalized()
	log := d.Log
	if log == nil {
		log = slog.Default()
	}

	var wait time.Duration
	for {
		started := d.now()
		code, err := d.Worker(ctx)
		if ctx.Err() != nil {
			// Stopping was the instruction, so however the worker took it is
			// not a failure to recover from.
			return nil
		}
		uptime := d.now().Sub(started)

		switch {
		case code == 0 && err == nil:
			log.Info("worker exited cleanly, dispatcher stopping", "uptime", uptime.Round(time.Millisecond))
			return nil
		case code == ExitRestart:
			// Asked for, not gone wrong: the pacing of past failures has
			// nothing to say about it, and making the operator wait out a
			// backoff for their own settings change would be absurd.
			log.Info("worker asked to be replaced", "uptime", uptime.Round(time.Millisecond))
			wait = 0
		default:
			if uptime >= policy.Steady {
				wait = policy.Min
			} else {
				wait = policy.next(wait)
			}
			log.Error("worker exited, restarting",
				"code", code, "error", err, "uptime", uptime.Round(time.Millisecond), "in", wait)
		}

		if wait > 0 && !d.pause(ctx, wait) {
			return nil
		}
	}
}

// pause waits out the restart delay, reporting false when the dispatcher was
// stopped while waiting.
func (d *Dispatcher) pause(ctx context.Context, delay time.Duration) bool {
	if d.Wait != nil {
		return d.Wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// recordName is the file a running dispatcher leaves under the agent home.
const recordName = "serve.json"

// Record is how a running dispatcher can be found again by a `coddy serve
// status`, `stop` or `restart` typed in some other terminal.
//
// It names the dispatcher rather than the worker on purpose: the worker is
// replaced whenever it dies, so its pid is stale the moment it is written, and
// stopping it would only get another one started.
type Record struct {
	// PID is the dispatcher process.
	PID int `json:"pid"`
	// StartedAt is when the dispatcher started, for the operator reading a
	// status line.
	StartedAt time.Time `json:"started_at"`
	// Identity is the creation time the operating system stamped on the
	// process, which is what tells the recorded process apart from a stranger
	// that inherited its pid. It is zero on platforms that answer liveness
	// without it, so it is never shown and never compared by hand.
	Identity time.Time `json:"identity,omitempty"`
	// Version is the binary that was running, so an operator can see that an
	// update has not been picked up yet.
	Version string `json:"version"`
	// Config is the file the daemon was started against.
	Config string `json:"config"`
	// Log is where its output goes, which is the first thing anybody asks for.
	Log string `json:"log"`
	// Args are the `coddy serve` arguments it was started with, so a restart
	// brings back the same daemon rather than a default one.
	Args []string `json:"args,omitempty"`
	// WorkerPID is the process the surfaces are actually running in, or 0 while
	// there is none - the last one failed and the dispatcher is waiting to try
	// again. A dispatcher that is up says nothing about whether anything is
	// being served, which is the question an operator is really asking.
	WorkerPID int `json:"worker_pid,omitempty"`
	// WorkerIdentity is the worker's creation time, kept for the same reason
	// Identity is kept for the dispatcher: a successor that has to clear an
	// abandoned worker must be sure of what it is killing.
	WorkerIdentity time.Time `json:"worker_identity,omitempty"`
	// LastError is why the most recent worker went away. It is kept after a
	// replacement comes up, because "it is running now, and here is what went
	// wrong before" is more use than either half alone.
	LastError string `json:"last_error,omitempty"`
}

// Serving reports whether a worker is running right now, which is what decides
// whether anything is answering.
func (r Record) Serving() bool {
	return r.Running() && r.WorkerPID > 0
}

// RecordPath is where the record for one agent home lives.
func RecordPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, recordName)
}

// WriteRecord saves the record for a home, creating the home if it is not there.
func WriteRecord(home string, r Record) error {
	path := RecordPath(home)
	if path == "" {
		return errors.New("serve: no agent home to record the dispatcher in")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// ErrNoDispatcher is returned when no dispatcher was ever recorded for a home.
var ErrNoDispatcher = errors.New("no coddy serve dispatcher is recorded for this agent home")

// ReadRecord loads the record for a home. A missing file is ErrNoDispatcher; a
// corrupt one is reported as it is, because silently treating it as absent would
// leave a running daemon nobody can stop.
func ReadRecord(home string) (Record, error) {
	path := RecordPath(home)
	if path == "" {
		return Record{}, ErrNoDispatcher
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNoDispatcher
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(body, &r); err != nil {
		return Record{}, fmt.Errorf("read %s: %w", path, err)
	}
	return r, nil
}

// RemoveRecord deletes the record. A record that is not there is not an error.
func RemoveRecord(home string) error {
	path := RecordPath(home)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Running reports whether the recorded dispatcher is still on this machine.
//
// A record outlives the process it describes whenever the machine went down
// without giving the daemon a chance to clean up, so every reader has to ask
// this rather than trust the file.
func (r Record) Running() bool {
	return platform.ProcessAlive(r.PID, r.Identity)
}

// Stop asks the recorded dispatcher to shut down and waits for it to go.
func (r Record) Stop(grace time.Duration) error {
	if !r.Running() {
		return nil
	}
	return platform.StopProcess(r.PID, r.Identity, grace)
}

// ProcessStartedAt reads the identity of a process this binary just started, so
// the record can tell it apart from a stranger that later inherits its pid.
func ProcessStartedAt(pid int) time.Time {
	return platform.ProcessStartedAt(pid)
}
