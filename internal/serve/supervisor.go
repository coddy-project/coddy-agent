package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// restartDrain bounds how long a surface may take to stop before the supervisor
// starts its replacement anyway. A Telegram turn that is still generating gets a
// chance to finish; one that is wedged does not get to block the token rotation
// that was the point of the restart.
const restartDrain = 30 * time.Second

// ErrRestartRequested ends Run when the configuration moved something no running
// process can adopt - the address a listener is bound to - and there is a
// dispatcher behind this one to bring a replacement up on the new value.
var ErrRestartRequested = errors.New("configuration change needs a fresh process")

// Supervisor runs a set of subsystems until the context ends, and rebuilds the
// ones whose configuration moved underneath them.
type Supervisor struct {
	log  *slog.Logger
	subs []Subsystem

	// Restartable is whether something is waiting to start this process again.
	// With a dispatcher behind it, a configuration change a listener cannot
	// adopt ends the process and comes back on the new address; without one,
	// exiting would take the daemon down for good, so the change is reported
	// and the operator restarts when it suits them.
	Restartable bool

	// Sessionless is a process started with no session store and no manager,
	// because nothing it ran at start needed one (a bare swarm relay). A
	// surface that needs sessions is not started in it by a reload: the
	// process asks to be replaced when something will start it again, and says
	// a restart is due otherwise.
	Sessionless bool

	mu      sync.Mutex
	running map[Kind]*instance
	// applied is the configuration the running set was last brought in line
	// with, nil until the surfaces of the first one have been started. Status
	// reads a configuration the supervisor has not applied yet as pending.
	applied *config.Config
}

// instance is one live subsystem: the context that stops it, the goroutine that
// is in it, and the configuration it was built from.
type instance struct {
	cancel      context.CancelFunc
	done        chan struct{}
	fingerprint string
	restartKey  string
	auxiliary   bool
}

// NewSupervisor prepares a supervisor over every subsystem this binary knows
// about, enabled or not.
//
// The disabled ones matter: a configuration change can turn one on, and the
// supervisor can only start a surface it was told exists. Resolve stays the
// pre-flight that refuses an impossible configuration before anything binds.
func NewSupervisor(log *slog.Logger, subs []Subsystem) *Supervisor {
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{log: log, subs: subs, running: make(map[Kind]*instance)}
}

// Run starts every subsystem and blocks until ctx is cancelled or one of them
// fails.
//
// A failure is fatal for the whole process on purpose. Half of `coddy serve`
// still answering is worse than none of it: the operator's supervisor restarts
// a process that exited, and nobody notices one that is quietly missing the
// surface they rely on. An auxiliary subsystem is the exception: nobody is
// served through it, so it fails alone.
//
// cfg is the configuration the surfaces are being started from: it seeds the
// restart fingerprints, so the first reload only rebuilds what actually moved.
// reloads carries configurations as they are replaced and may be nil, in which
// case the process runs whatever it started with.
func (s *Supervisor) Run(ctx context.Context, cfg *config.Config, reloads <-chan *config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	failures := make(chan error, len(s.subs))
	for _, sub := range s.subs {
		if sub.enabled(cfg) && sub.Available {
			s.start(ctx, sub, cfg, failures)
		}
	}
	s.markApplied(cfg)

	var runErr error
	for {
		select {
		case <-ctx.Done():
			return s.shutdown(runErr)
		case err := <-failures:
			if err != nil && runErr == nil {
				runErr = err
			}
			cancel()
		case next, ok := <-reloads:
			if !ok {
				reloads = nil
				continue
			}
			if s.applyConfig(ctx, next, failures) {
				// The surfaces are stopped in the same order a signal stops
				// them, and the request is what the caller exits with.
				if err := s.shutdown(runErr); err != nil {
					return err
				}
				return ErrRestartRequested
			}
		}
	}
}

// start launches one subsystem under its own child context.
func (s *Supervisor) start(ctx context.Context, sub Subsystem, cfg *config.Config, failures chan<- error) {
	child, cancel := context.WithCancel(ctx)
	inst := &instance{
		cancel:      cancel,
		done:        make(chan struct{}),
		fingerprint: sub.fingerprint(cfg),
		restartKey:  sub.restartKey(cfg),
		auxiliary:   sub.Auxiliary,
	}
	s.mu.Lock()
	s.running[sub.Kind] = inst
	s.mu.Unlock()

	s.log.Info("subsystem starting", "subsystem", string(sub.Kind))
	go func() {
		defer close(inst.done)
		err := runGuarded(child, sub, cfg)
		if child.Err() != nil {
			// Stopping was the instruction, so whatever the surface returned on
			// the way out is noise, not a failure.
			s.log.Info("subsystem stopped", "subsystem", string(sub.Kind))
			return
		}
		if err == nil {
			err = fmt.Errorf("%s stopped on its own", sub.Kind)
		}
		s.log.Error("subsystem failed", "subsystem", string(sub.Kind), "error", err)
		if sub.Auxiliary {
			// Nobody is served through it, so its failure - an exporter moved
			// onto a port that is taken - ends it alone; the next configuration
			// that enables it starts it again.
			s.mu.Lock()
			if s.running[sub.Kind] == inst {
				delete(s.running, sub.Kind)
			}
			s.mu.Unlock()
			return
		}
		failures <- fmt.Errorf("%s: %w", sub.Kind, err)
	}()
}

// runGuarded turns a panic inside a surface into an ordinary error, so one
// misbehaving subsystem takes the process down through the same path as a
// failed listen instead of unwinding past every other subsystem's cleanup.
func runGuarded(ctx context.Context, sub Subsystem, cfg *config.Config) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	if sub.Run == nil {
		return errors.New("subsystem has no Run")
	}
	return sub.Run(ctx, cfg)
}

// applyConfig brings the running set in line with a configuration that was
// replaced underneath it: a surface turned on starts, one turned off stops, one
// whose settings moved is rebuilt, and one nobody touched is left alone.
//
// Nothing here may end the process. A configuration arrives from whoever can
// write it - the settings screen, the agent's own config_commit tool, an
// operator reaching this node through a relay - and a writer that could make
// the daemon exit would be holding a remote kill switch. So a surface this
// binary cannot run is refused loudly and skipped, where the same configuration
// at startup is a hard error.
func (s *Supervisor) applyConfig(ctx context.Context, cfg *config.Config, failures chan<- error) (restart bool) {
	if cfg == nil {
		return false
	}
	// A refused change counts as applied: the process stays as it is.
	defer s.markApplied(cfg)
	// What only a fresh process can adopt is weighed before anything moves.
	// Applying the rest first would leave a refused change half done - a
	// surface it turned off already stopped, the others as they were - and a
	// process that matches neither configuration. A change the replacement can
	// start on is left to it whole: it reads the same file.
	if kind, ok := s.needsFreshProcess(cfg); ok {
		if !s.canRunFresh(cfg, kind) {
			return false
		}
		if s.Restartable {
			s.log.Info("configuration change needs a fresh process, restarting",
				"subsystem", string(kind))
			return true
		}
	}
	for _, sub := range s.subs {
		s.mu.Lock()
		inst := s.running[sub.Kind]
		s.mu.Unlock()
		want := sub.enabled(cfg)

		switch {
		case inst == nil && !want:
			// Off and asked to stay off.
		case inst == nil && want:
			if !sub.Available {
				s.log.Error("subsystem enabled by a configuration change but missing from this build",
					"subsystem", string(sub.Kind), "key", sub.ConfigKey,
					"hint", "rebuild with -tags "+sub.BuildTag)
				continue
			}
			if sub.NeedsSessions && s.Sessionless {
				// Started on no manager it would fail at once, and its failure
				// ends every surface of the process, the relay included. A
				// process something would start again was replaced above.
				s.log.Error("subsystem enabled by a configuration change needs the session store this process was started without",
					"subsystem", string(sub.Kind), "key", sub.ConfigKey,
					"hint", "restart coddy serve to apply")
				continue
			}
			s.log.Info("subsystem enabled by a configuration change", "subsystem", string(sub.Kind))
			s.start(ctx, sub, cfg, failures)
		case inst != nil && !want:
			// Stopping the last surface would leave a process that runs nothing
			// and can no longer be reached to say otherwise - the state
			// Resolve refuses at startup. Keep it and say so instead, the way
			// a listener whose bind address moved is kept. An auxiliary
			// subsystem can always stop, and it does not count as a surface.
			if !sub.Auxiliary && s.runningSurfaces() == 1 {
				s.log.Error("refusing to stop the only running surface",
					"subsystem", string(sub.Kind), "key", sub.ConfigKey,
					"hint", "a process with nothing enabled cannot be reached; enable another subsystem first, or restart coddy serve")
				continue
			}
			s.log.Info("subsystem disabled by a configuration change", "subsystem", string(sub.Kind))
			s.stop(sub.Kind, inst)
		case sub.restartKey(cfg) != inst.restartKey:
			// A listener cannot be moved under the caller that is talking
			// through it. With a dispatcher behind this process the whole thing
			// came back on the new address above, which is how an operator
			// moves a port from the settings screen of the very server they
			// are moving; without one the operator is told.
			s.log.Info("subsystem listen settings changed, restart required",
				"subsystem", string(sub.Kind), "hint", "restart coddy serve to apply")
		case sub.Fingerprint == nil:
			// Nothing about this surface can be adopted in place and nothing
			// about it needs a fresh process either, so there is nothing to do.
		case sub.fingerprint(cfg) != inst.fingerprint:
			s.log.Info("subsystem settings changed, restarting", "subsystem", string(sub.Kind))
			s.stop(sub.Kind, inst)
			s.start(ctx, sub, cfg, failures)
		}
	}
	return false
}

// markApplied records cfg as the configuration the running set was last
// brought in line with.
func (s *Supervisor) markApplied(cfg *config.Config) {
	s.mu.Lock()
	s.applied = cfg
	s.mu.Unlock()
}

// Status reads the subsystem table off the running set and live, the
// configuration in force. Nothing is recorded as a change comes in, so a read
// right after a save sees a listener's new address before the supervisor has
// been handed the configuration, and moving the address back clears it.
//
// A change only a fresh process can adopt is pending, as needsFreshProcess
// decides it: a listener whose address live moved, or a surface that runs
// turns in a process started without a session store. It needs a manual
// restart unless a dispatcher would restart the process by itself, which it
// does only into a configuration the pre-flight accepts (the check applyConfig
// makes). A listener live no longer enables may simply stop, so it is never
// pending.
func (s *Supervisor) Status(live *config.Config) (subs []SubsystemStatus, restartRequired bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := false
	for _, sub := range s.subs {
		inst := s.running[sub.Kind]
		want := sub.enabled(live)
		if inst == nil && !want {
			continue
		}
		row := SubsystemStatus{Kind: sub.Kind}
		switch {
		case inst != nil && want:
			row.State = StateRunning
		case inst != nil:
			row.State = StateRetained
		case s.applied != live:
			row.State = StateStarting
		default:
			row.State = StateStopped
		}
		switch {
		case inst != nil && want && sub.restartKey(live) != inst.restartKey,
			inst == nil && want && sub.Available && sub.NeedsSessions && s.Sessionless:
			pending = true
		}
		subs = append(subs, row)
	}
	if !pending {
		return subs, false
	}
	if !s.Restartable {
		return subs, true
	}
	_, err := Resolve(live, s.subs)
	return subs, err != nil
}

// needsFreshProcess names a surface cfg asks for that this process cannot
// give it in place: one that runs agent turns while the process was started
// without a session store, or a listener whose address moved. ok is false when
// every change can be applied where it stands.
func (s *Supervisor) needsFreshProcess(cfg *config.Config) (kind Kind, ok bool) {
	for _, sub := range s.subs {
		if !sub.enabled(cfg) {
			continue
		}
		s.mu.Lock()
		inst := s.running[sub.Kind]
		s.mu.Unlock()
		switch {
		case inst == nil:
			if sub.Available && sub.NeedsSessions && s.Sessionless {
				return sub.Kind, true
			}
		case sub.restartKey(cfg) != inst.restartKey:
			return sub.Kind, true
		}
	}
	return "", false
}

// canRunFresh reports whether a fresh process could start on cfg, and says why
// not when it could not. A configuration change that needs a new process is
// only worth one the new process can run: one its pre-flight (Resolve)
// refuses would be restarted into again and again with nothing serving, so
// that change is refused whole and every running surface is left as it was.
func (s *Supervisor) canRunFresh(cfg *config.Config, kind Kind) bool {
	if _, err := Resolve(cfg, s.subs); err != nil {
		s.log.Error("configuration change refused: a fresh process could not start on it",
			"subsystem", string(kind), "error", err)
		return false
	}
	return true
}

// runningSurfaces reports how many subsystems that are not auxiliary are live
// right now.
func (s *Supervisor) runningSurfaces() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, inst := range s.running {
		if !inst.auxiliary {
			n++
		}
	}
	return n
}

// stop cancels one instance and waits for its goroutine, bounded by restartDrain.
func (s *Supervisor) stop(kind Kind, inst *instance) {
	inst.cancel()
	select {
	case <-inst.done:
	case <-time.After(restartDrain):
		s.log.Warn("subsystem did not stop in time", "subsystem", string(kind), "waited", restartDrain)
	}
	s.mu.Lock()
	if s.running[kind] == inst {
		delete(s.running, kind)
	}
	s.mu.Unlock()
}

// shutdown stops every instance and reports the first failure.
//
// It walks the declared order backwards, which puts the surfaces that bring
// work in - the messenger poller, the relay, the cron daemon - ahead of the
// HTTP server. Intake stops first, the turns already running drain against a
// session store nobody has torn down yet, and the API is the last thing to go.
func (s *Supervisor) shutdown(runErr error) error {
	for i := len(s.subs) - 1; i >= 0; i-- {
		kind := s.subs[i].Kind
		s.mu.Lock()
		inst := s.running[kind]
		s.mu.Unlock()
		if inst == nil {
			continue
		}
		s.stop(kind, inst)
	}
	return runErr
}
