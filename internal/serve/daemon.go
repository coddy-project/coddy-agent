package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// EnvRole tells a coddy that re-executed itself which half of the daemon it is.
// It is an environment variable rather than a flag because nobody types it: the
// two roles exist between a `coddy serve --daemon` and the processes it starts.
const EnvRole = "CODDY_SERVE_ROLE"

// The roles a `coddy serve` process can be started in.
const (
	// RoleDispatcher supervises workers and owns the record.
	RoleDispatcher = "dispatcher"
	// RoleWorker runs the subsystems and may ask to be replaced.
	RoleWorker = "worker"
)

// workerDrain bounds how long a worker is given to finish what it is doing when
// the dispatcher is stopping it. A turn that is still generating gets to finish;
// one that is wedged does not get to keep the operator waiting.
const workerDrain = 30 * time.Second

// startupGrace bounds how long `coddy serve --daemon` waits for the dispatcher
// it started to record itself before reporting that it did not come up.
const startupGrace = 10 * time.Second

// Role reports which half of the daemon this process was started as, or "" for
// an ordinary foreground `coddy serve`.
func Role() string { return strings.TrimSpace(os.Getenv(EnvRole)) }

// Supervised reports whether something is waiting to start this process again.
// It is what decides between exiting for a replacement and reporting that a
// restart is due.
func Supervised() bool { return Role() == RoleWorker }

// ExitCodeError carries the status a command wants the process to exit with,
// for the cases where "failed" is not the whole story - a worker asking its
// dispatcher for a replacement exits with ExitRestart, not with 1.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e ExitCodeError) Error() string { return e.Err.Error() }
func (e ExitCodeError) Unwrap() error { return e.Err }

// DaemonOptions is what both halves of the daemon need to know.
type DaemonOptions struct {
	// Home is the agent home the record and the default log live under.
	Home string
	// Config is the configuration file the daemon was started against, recorded
	// so an operator can see which one a running daemon is serving.
	Config string
	// LogPath is where the daemon's output goes. Empty puts it under the home.
	LogPath string
	// Args are the `coddy serve` arguments to bring the daemon back with,
	// already stripped of the ones that only make sense once (--daemon).
	Args []string
	// Policy paces worker restarts. The zero value is the default pacing.
	Policy RestartPolicy
	// Log is the dispatcher's own logger.
	Log *slog.Logger
}

// DefaultLogPath is where a daemon writes when the operator named no file.
func DefaultLogPath(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "logs", "serve.log")
}

func (o DaemonOptions) logPath() string {
	if p := strings.TrimSpace(o.LogPath); p != "" {
		return p
	}
	return DefaultLogPath(o.Home)
}

func (o DaemonOptions) logger() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

// ErrAlreadyRunning is returned when a dispatcher is already up for this home.
type ErrAlreadyRunning struct{ Record Record }

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("a coddy serve dispatcher is already running (pid %d); stop it with `coddy serve stop` or use `coddy serve restart`", e.Record.PID)
}

// StartDetached launches the dispatcher in the background and waits until it has
// recorded itself.
//
// The wait is the point: a daemon whose configuration is wrong, or whose port is
// taken, would otherwise exit into a log file nobody is looking at while the
// terminal that started it reported success. Waiting for the record turns that
// into a failure the operator sees, with the log to read.
func StartDetached(opts DaemonOptions) (Record, error) {
	if existing, err := ReadRecord(opts.Home); err == nil && existing.Running() {
		return Record{}, &ErrAlreadyRunning{Record: existing}
	}
	exe, err := os.Executable()
	if err != nil {
		return Record{}, fmt.Errorf("locate the coddy binary: %w", err)
	}
	logPath := opts.logPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return Record{}, fmt.Errorf("create the log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Record{}, fmt.Errorf("open %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, append([]string{"serve"}, opts.Args...)...)
	cmd.Env = append(os.Environ(), EnvRole+"="+RoleDispatcher)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	platform.DetachFromTerminal(cmd)
	if err := cmd.Start(); err != nil {
		return Record{}, fmt.Errorf("start the dispatcher: %w", err)
	}
	pid := cmd.Process.Pid
	// Nothing waits for a detached daemon, and a child nobody reaps is a zombie
	// for as long as this process lives - which for a CLI is a moment, but the
	// handle is released explicitly rather than left to that coincidence.
	_ = cmd.Process.Release()

	// Wait for the dispatcher to have started a worker and for that worker to
	// have either come up or failed, so the terminal that typed the command sees
	// what an operator would otherwise only find in the log.
	deadline := time.Now().Add(startupGrace)
	for time.Now().Before(deadline) {
		rec, err := ReadRecord(opts.Home)
		if err == nil && rec.PID == pid && (rec.WorkerPID > 0 || rec.LastError != "") {
			return rec, nil
		}
		if !platform.ProcessAlive(pid, time.Time{}) {
			return Record{}, fmt.Errorf("the dispatcher exited during startup; see %s", logPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return Record{}, fmt.Errorf("the dispatcher did not come up within %s; see %s", startupGrace, logPath)
}

// RunDispatcher is the dispatcher process: it records itself, keeps a worker
// running, and cleans up after itself when it is told to stop.
func RunDispatcher(ctx context.Context, opts DaemonOptions) error {
	log := opts.logger()
	reapAbandonedWorker(opts.Home, log)
	self := Record{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		Identity:  platform.ProcessStartedAt(os.Getpid()),
		Version:   version.Get(),
		Config:    opts.Config,
		Log:       opts.logPath(),
		Args:      opts.Args,
	}
	if err := WriteRecord(opts.Home, self); err != nil {
		return fmt.Errorf("record the dispatcher: %w", err)
	}
	defer func() {
		// Only if it is still ours: a `coddy serve restart` that started a
		// replacement before this one finished shutting down has already
		// written its own, and removing that would lose the running daemon.
		if rec, err := ReadRecord(opts.Home); err == nil && rec.PID == self.PID {
			_ = RemoveRecord(opts.Home)
		}
	}()

	log.Info("dispatcher started", "pid", self.PID, "version", self.Version, "config", self.Config, "log", self.Log)

	// The record follows the worker as well as the dispatcher. A dispatcher that
	// is up says nothing about whether anything is being served: a port somebody
	// else holds leaves it faithfully restarting a worker that cannot start, and
	// an operator who only saw "running" would go looking in the wrong place.
	var recordMu sync.Mutex
	note := func(mutate func(*Record)) {
		recordMu.Lock()
		defer recordMu.Unlock()
		mutate(&self)
		if err := WriteRecord(opts.Home, self); err != nil {
			log.Warn("update the dispatcher record", "error", err)
		}
	}
	d := &Dispatcher{
		Worker: func(ctx context.Context) (int, error) {
			code, err := runWorkerProcess(ctx, opts, func(pid int) {
				note(func(r *Record) {
					r.WorkerPID = pid
					r.WorkerIdentity = platform.ProcessStartedAt(pid)
				})
			})
			note(func(r *Record) {
				r.WorkerPID = 0
				r.WorkerIdentity = time.Time{}
				if err != nil {
					// The worker's own diagnosis went to the log with its
					// stderr; a status of 1 on its own would send the operator
					// nowhere, so the record says where the rest of it is.
					r.LastError = fmt.Sprintf("worker exited with status %d, see %s", code, r.Log)
				}
			})
			return code, err
		},
		Policy: opts.Policy,
		Log:    log,
	}
	err := d.Run(ctx)
	log.Info("dispatcher stopped")
	return err
}

// reapAbandonedWorker ends a worker its own dispatcher is no longer around to
// stop.
//
// A dispatcher that was killed outright - `kill -9`, an OOM - leaves its worker
// running in a process group of its own, holding the port and answering
// requests while `coddy serve status` reports that nothing is running. The next
// dispatcher would then restart forever against an address it can never bind.
// Its predecessor's record is the only trace of that process, so it is read
// before it is overwritten.
func reapAbandonedWorker(home string, log *slog.Logger) {
	prev, err := ReadRecord(home)
	if err != nil || prev.WorkerPID <= 0 {
		return
	}
	if prev.Running() {
		// Its dispatcher is alive and owns it. StartDetached refuses a second
		// daemon for exactly this reason, so arriving here means somebody
		// started one by hand; taking its worker away would be worse than
		// leaving both alone.
		return
	}
	if !platform.ProcessGroupAlive(prev.WorkerPID, prev.WorkerIdentity) {
		return
	}
	log.Warn("stopping a worker left behind by a previous dispatcher", "pid", prev.WorkerPID)
	if err := platform.TerminateProcessGroupByPID(prev.WorkerPID, prev.WorkerIdentity, workerDrain); err != nil {
		log.Warn("stopping the abandoned worker", "pid", prev.WorkerPID, "error", err)
	}
}

// runWorkerProcess starts this same binary with the subsystems in it and waits
// for it to exit.
//
// A fresh process rather than a goroutine is what makes a crash survivable: the
// worker comes back built from the configuration on disk and from the binary on
// disk, so a panic that escaped a surface, an OOM kill, or a `coddy update` that
// replaced the executable are all recovered by the same mechanism.
func runWorkerProcess(ctx context.Context, opts DaemonOptions, onStart func(pid int)) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("locate the coddy binary: %w", err)
	}
	cmd := exec.Command(exe, append([]string{"serve"}, opts.Args...)...)
	cmd.Env = append(os.Environ(), EnvRole+"="+RoleWorker)
	// The dispatcher's own output is already the log file, so the worker writing
	// to the same descriptors is what puts both halves in one place.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Its own group, so stopping the worker reaches whatever it started - a
	// background shell task, an MCP server - rather than orphaning it.
	platform.DetachProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("start the worker: %w", err)
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return exitStatus(err), err
	case <-ctx.Done():
		if err := platform.TerminateProcessGroup(cmd, workerDrain); err != nil {
			opts.logger().Warn("stopping the worker", "error", err)
		}
		<-done
		return 0, nil
	}
}

// exitStatus reads the status a finished command exited with. A failure that
// carries no status - the process could not be waited for at all - is reported
// as 1, because a worker whose ending nobody understood is not one to stop over.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
