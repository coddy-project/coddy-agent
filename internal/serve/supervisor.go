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

// Supervisor runs a set of subsystems until the context ends, and rebuilds the
// ones whose configuration moved underneath them.
type Supervisor struct {
	log  *slog.Logger
	subs []Subsystem

	mu      sync.Mutex
	running map[Kind]*instance
}

// instance is one live subsystem: the context that stops it, the goroutine that
// is in it, and the configuration it was built from.
type instance struct {
	cancel      context.CancelFunc
	done        chan struct{}
	fingerprint string
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
// surface they rely on.
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
			s.applyConfig(ctx, next, failures)
		}
	}
}

// start launches one subsystem under its own child context.
func (s *Supervisor) start(ctx context.Context, sub Subsystem, cfg *config.Config, failures chan<- error) {
	child, cancel := context.WithCancel(ctx)
	inst := &instance{cancel: cancel, done: make(chan struct{}), fingerprint: sub.fingerprint(cfg)}
	s.mu.Lock()
	s.running[sub.Kind] = inst
	s.mu.Unlock()

	s.log.Info("subsystem starting", "subsystem", string(sub.Kind))
	go func() {
		defer close(inst.done)
		err := runGuarded(child, sub)
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
		failures <- fmt.Errorf("%s: %w", sub.Kind, err)
	}()
}

// runGuarded turns a panic inside a surface into an ordinary error, so one
// misbehaving subsystem takes the process down through the same path as a
// failed listen instead of unwinding past every other subsystem's cleanup.
func runGuarded(ctx context.Context, sub Subsystem) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	if sub.Run == nil {
		return errors.New("subsystem has no Run")
	}
	return sub.Run(ctx)
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
func (s *Supervisor) applyConfig(ctx context.Context, cfg *config.Config, failures chan<- error) {
	if cfg == nil {
		return
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
			s.log.Info("subsystem enabled by a configuration change", "subsystem", string(sub.Kind))
			s.start(ctx, sub, cfg, failures)
		case inst != nil && !want:
			// Stopping the last one would leave a process that runs nothing
			// and can no longer be reached to say otherwise - the state
			// Resolve refuses at startup. Keep it and say so instead, the way
			// a listener whose bind address moved is kept.
			if s.runningCount() == 1 {
				s.log.Error("refusing to stop the only running subsystem",
					"subsystem", string(sub.Kind), "key", sub.ConfigKey,
					"hint", "a process with nothing enabled cannot be reached; enable another subsystem first, or restart coddy serve")
				continue
			}
			s.log.Info("subsystem disabled by a configuration change", "subsystem", string(sub.Kind))
			s.stop(sub.Kind, inst)
		case sub.Fingerprint == nil:
			// A listener cannot be moved under the caller that is talking
			// through it; say so instead of pretending the edit took effect.
			s.log.Info("subsystem settings may have changed, restart required",
				"subsystem", string(sub.Kind), "hint", "restart coddy serve to apply")
		case sub.fingerprint(cfg) != inst.fingerprint:
			s.log.Info("subsystem settings changed, restarting", "subsystem", string(sub.Kind))
			s.stop(sub.Kind, inst)
			s.start(ctx, sub, cfg, failures)
		}
	}
}

// runningCount reports how many subsystems are live right now.
func (s *Supervisor) runningCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
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
