package serve

// Godog harness for features/serve_subsystems.feature: which surfaces one
// `coddy serve` process starts, what it refuses, and what a settings change
// rebuilds. The surfaces themselves are stand-ins, because the question here is
// the supervisor's, not any one server's.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type serveFeatureState struct {
	cfg   *config.Config
	subs  []Subsystem
	ready []Subsystem
	err   error

	mu     sync.Mutex
	starts map[Kind]int
	stops  map[Kind]int
	// startedWith is the configuration each surface was last started from.
	startedWith map[Kind]*config.Config

	sup     *Supervisor
	cancel  context.CancelFunc
	done    chan struct{}
	reloads chan *config.Config

	// restartable is whether this runtime has a dispatcher behind it, which is
	// what decides between exiting for a replacement and logging that a restart
	// is due.
	restartable bool
	// sessionless is a process started as a bare relay, which opened no
	// session store.
	sessionless bool
	runErr      error
}

func (s *serveFeatureState) reset() {
	s.stopSupervisor()
	s.cfg = &config.Config{}
	s.ready = nil
	s.err = nil
	s.starts = make(map[Kind]int)
	s.stops = make(map[Kind]int)
	s.startedWith = make(map[Kind]*config.Config)
	s.subs = s.describe(true)
}

func (s *serveFeatureState) stopSupervisor() {
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
		s.cancel = nil
	}
}

// describe builds the same four descriptors the CLI does, over surfaces that
// only record that they ran.
func (s *serveFeatureState) describe(gatewayAvailable bool) []Subsystem {
	block := func(kind Kind) func(context.Context, *config.Config) error {
		return func(ctx context.Context, cfg *config.Config) error {
			s.mu.Lock()
			s.starts[kind]++
			s.startedWith[kind] = cfg
			s.mu.Unlock()
			<-ctx.Done()
			s.mu.Lock()
			s.stops[kind]++
			s.mu.Unlock()
			return nil
		}
	}
	return []Subsystem{
		{
			Kind: KindHTTP, ConfigKey: "httpserver.enable", BuildTag: "http", Available: true,
			NeedsSessions: true,
			Enabled:       func(c *config.Config) bool { return c.HTTPServer.IsEnabled() },
			RestartKey:    func(c *config.Config) string { return c.HTTPServer.DefaultListenPortString() },
			Run:           block(KindHTTP),
		},
		{
			Kind: KindGateway, ConfigKey: "gateways.telegram.enable", BuildTag: "gateway", Available: gatewayAvailable,
			NeedsSessions: true,
			Enabled:       func(c *config.Config) bool { return c.Gateways.Telegram.Enabled },
			Fingerprint:   func(c *config.Config) string { return c.Gateways.Telegram.Token },
			Run:           block(KindGateway),
		},
		{
			Kind: KindSwarm, ConfigKey: "swarm.enable", BuildTag: "swarm", Available: true,
			Enabled: func(c *config.Config) bool { return c.Swarm.Enabled },
			Run:     block(KindSwarm),
		},
		{
			Kind: KindScheduler, ConfigKey: "scheduler.enable", BuildTag: "scheduler", Available: true,
			NeedsSessions: true,
			Enabled:       func(c *config.Config) bool { return c.Scheduler.Enabled },
			Fingerprint:   func(c *config.Config) string { return c.Scheduler.Timeout },
			Run:           block(KindScheduler),
		},
	}
}

func (s *serveFeatureState) startCount(kind Kind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts[kind]
}

func (s *serveFeatureState) stopCount(kind Kind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops[kind]
}

// settle returns once the supervisor is done with every configuration sent
// before it, instead of sleeping and hoping it was. The reloads channel holds
// one configuration and the supervisor takes the next only after applying the
// last, so the second of two sends of cfg - a configuration that changes
// nothing - goes through only when everything before them has been applied.
func (s *serveFeatureState) settle(cfg *config.Config) error {
	for i := 0; i < 2; i++ {
		select {
		case s.reloads <- cfg:
		case <-s.done:
			return fmt.Errorf("the runtime stopped: %v", s.runErr)
		case <-time.After(5 * time.Second):
			return errors.New("the supervisor did not take the next configuration")
		}
	}
	return nil
}

func (s *serveFeatureState) kind(name string) (Kind, error) {
	switch Kind(name) {
	case KindHTTP, KindGateway, KindSwarm, KindScheduler:
		return Kind(name), nil
	}
	return "", fmt.Errorf("unknown subsystem %q", name)
}

// --- Given ---

func (s *serveFeatureState) configWithoutHTTPSection() error {
	s.reset()
	return nil
}

func (s *serveFeatureState) configEnablingGatewayAndScheduler() error {
	s.reset()
	s.cfg.Gateways.Telegram.Enabled = true
	s.cfg.Scheduler.Enabled = true
	return nil
}

func (s *serveFeatureState) configHTTPDisabledGatewayEnabled() error {
	s.reset()
	off := false
	s.cfg.HTTPServer.Enabled = &off
	s.cfg.Gateways.Telegram.Enabled = true
	return nil
}

func (s *serveFeatureState) configEnablingGateway() error {
	s.reset()
	s.cfg.Gateways.Telegram.Enabled = true
	off := false
	s.cfg.HTTPServer.Enabled = &off
	return nil
}

func (s *serveFeatureState) binaryWithoutGateway() error {
	s.subs = s.describe(false)
	return nil
}

func (s *serveFeatureState) configWithEverythingDisabled() error {
	s.reset()
	off := false
	s.cfg.HTTPServer.Enabled = &off
	return nil
}

func (s *serveFeatureState) runningRuntime() error {
	s.reset()
	s.cfg.Gateways.Telegram.Enabled = true
	s.cfg.Gateways.Telegram.Token = "first-token"
	return s.launch()
}

// launch resolves the current config and runs the supervisor over every
// descriptor, the way the CLI does.
func (s *serveFeatureState) launch() error {
	ready, err := Resolve(s.cfg, s.subs)
	if err != nil {
		return err
	}
	s.ready = ready

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.reloads = make(chan *config.Config, 1)
	s.sup = NewSupervisor(slog.New(slog.NewTextHandler(io.Discard, nil)), s.subs)
	s.sup.Restartable = s.restartable
	s.sup.Sessionless = s.sessionless
	go func() {
		defer close(s.done)
		s.runErr = s.sup.Run(ctx, s.cfg, s.reloads)
	}()
	return waitFor(func() bool {
		for _, sub := range ready {
			if s.startCount(sub.Kind) != 1 {
				return false
			}
		}
		return true
	})
}

// --- When ---

func (s *serveFeatureState) runningRuntimeHTTPOnly() error {
	s.reset()
	return s.launch()
}

func (s *serveFeatureState) httpDisabled() error {
	s.reload(func(c *config.Config) {
		off := false
		c.HTTPServer.Enabled = &off
	})
	// Nothing should happen, so there is no edge to wait for; give the
	// supervisor a moment to have done the wrong thing if it were going to.
	time.Sleep(200 * time.Millisecond)
	return nil
}

func (s *serveFeatureState) resolveSubsystems() error {
	s.ready, s.err = Resolve(s.cfg, s.subs)
	return nil
}

func (s *serveFeatureState) resolveListenAddress() error {
	return nil
}

// reload publishes a configuration the way the session manager does.
func (s *serveFeatureState) reload(mutate func(*config.Config)) *config.Config {
	next := &config.Config{}
	*next = *s.cfg
	mutate(next)
	s.cfg = next
	s.reloads <- next
	return next
}

func (s *serveFeatureState) schedulerEnabled() error {
	s.reload(func(c *config.Config) { c.Scheduler.Enabled = true })
	return waitFor(func() bool { return s.startCount(KindScheduler) == 1 })
}

func (s *serveFeatureState) gatewayDisabled() error {
	s.reload(func(c *config.Config) { c.Gateways.Telegram.Enabled = false })
	return waitFor(func() bool { return !s.isRunning(KindGateway) })
}

func (s *serveFeatureState) isRunning(kind Kind) bool {
	s.sup.mu.Lock()
	defer s.sup.mu.Unlock()
	return s.sup.running[kind] != nil
}

func (s *serveFeatureState) subsystemRunning(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	if !s.isRunning(kind) {
		return fmt.Errorf("%s is not running", kind)
	}
	return nil
}

func (s *serveFeatureState) subsystemStopped(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	if s.isRunning(kind) {
		return fmt.Errorf("%s is still running", kind)
	}
	return nil
}

// httpBackAtPort turns the HTTP surface off, and on again at another port,
// the way an operator moves a surface they took down for a while.
func (s *serveFeatureState) httpBackAtPort(port int) error {
	s.reload(func(c *config.Config) {
		off := false
		c.HTTPServer.Enabled = &off
	})
	if err := waitFor(func() bool { return !s.isRunning(KindHTTP) }); err != nil {
		return fmt.Errorf("the httpserver was not stopped: %w", err)
	}
	s.reload(func(c *config.Config) {
		on := true
		c.HTTPServer.Enabled = &on
		c.HTTPServer.Port = port
	})
	return waitFor(func() bool { return s.startCount(KindHTTP) == 2 })
}

func (s *serveFeatureState) startedFromPort(name string, port string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	cfg := s.startedWith[kind]
	s.mu.Unlock()
	if cfg == nil {
		return fmt.Errorf("%s was started from no configuration", kind)
	}
	if got := cfg.HTTPServer.DefaultListenPortString(); got != port {
		return fmt.Errorf("%s was started from a configuration with port %s, want %s", kind, got, port)
	}
	return nil
}

func (s *serveFeatureState) tokenChanged() error {
	s.reload(func(c *config.Config) { c.Gateways.Telegram.Token = "rotated-token" })
	return waitFor(func() bool { return s.startCount(KindGateway) == 2 })
}

// --- Then ---

func (s *serveFeatureState) subsystemEnabled(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	if s.err != nil {
		return fmt.Errorf("resolving failed: %w", s.err)
	}
	for _, sub := range s.ready {
		if sub.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("%s is not among the enabled subsystems", kind)
}

func (s *serveFeatureState) subsystemDisabled(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	for _, sub := range s.ready {
		if sub.Kind == kind {
			return fmt.Errorf("%s was started although the config leaves it off", kind)
		}
	}
	return nil
}

func (s *serveFeatureState) listenAddressIs(want string) error {
	got := s.cfg.HTTPServer.DefaultListenHost() + ":" + s.cfg.HTTPServer.DefaultListenPortString()
	if got != want {
		return fmt.Errorf("listen address = %q, want %q", got, want)
	}
	return nil
}

func (s *serveFeatureState) resolvingFailsNamingTag(tag string) error {
	if s.err == nil {
		return fmt.Errorf("resolving succeeded, expected a refusal naming -tags %s", tag)
	}
	if !strings.Contains(s.err.Error(), "-tags "+tag) {
		return fmt.Errorf("error %q does not name the build tag %q", s.err, tag)
	}
	return nil
}

func (s *serveFeatureState) resolvingAsksForASubsystem() error {
	if s.err == nil {
		return fmt.Errorf("resolving succeeded although nothing is enabled")
	}
	if !strings.Contains(s.err.Error(), "no subsystem is enabled") {
		return fmt.Errorf("error %q does not say that nothing is enabled", s.err)
	}
	return nil
}

func (s *serveFeatureState) subsystemRestarted(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	if got := s.startCount(kind); got != 2 {
		return fmt.Errorf("%s started %d times, want a restart (2)", kind, got)
	}
	return nil
}

func (s *serveFeatureState) subsystemKeepsRunning(name string) error {
	kind, err := s.kind(name)
	if err != nil {
		return err
	}
	if got := s.startCount(kind); got != 1 {
		return fmt.Errorf("%s started %d times, want it left alone (1)", kind, got)
	}
	return nil
}

func waitFor(cond func() bool) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within the deadline")
}

func initializeServeScenario(sc *godog.ScenarioContext) {
	s := &serveFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.stopSupervisor()
		return ctx, err
	})

	sc.Step(`^a config with no httpserver section$`, s.configWithoutHTTPSection)
	sc.Step(`^a config enabling the telegram gateway and the scheduler$`, s.configEnablingGatewayAndScheduler)
	sc.Step(`^a config with httpserver disabled and the telegram gateway enabled$`, s.configHTTPDisabledGatewayEnabled)
	sc.Step(`^a config enabling the telegram gateway$`, s.configEnablingGateway)
	sc.Step(`^a binary built without gateway support$`, s.binaryWithoutGateway)
	sc.Step(`^a config with every subsystem disabled$`, s.configWithEverythingDisabled)
	sc.Step(`^a running runtime with the httpserver and the telegram gateway enabled$`, s.runningRuntime)
	sc.Step(`^a running runtime with only the httpserver enabled$`, s.runningRuntimeHTTPOnly)

	sc.Step(`^the runtime resolves which subsystems to start$`, s.resolveSubsystems)
	sc.Step(`^the runtime resolves the httpserver listen address$`, s.resolveListenAddress)
	sc.Step(`^the telegram token is changed through the configuration$`, s.tokenChanged)
	sc.Step(`^the scheduler is enabled through the configuration$`, s.schedulerEnabled)
	sc.Step(`^the telegram gateway is disabled through the configuration$`, s.gatewayDisabled)
	sc.Step(`^the httpserver is disabled through the configuration$`, s.httpDisabled)
	sc.Step(`^the httpserver is turned off and back on at port (\d+) through the configuration$`, s.httpBackAtPort)

	sc.Step(`^the "([^"]*)" subsystem is enabled$`, s.subsystemEnabled)
	sc.Step(`^the "([^"]*)" subsystem is disabled$`, s.subsystemDisabled)
	sc.Step(`^the listen address is "([^"]*)"$`, s.listenAddressIs)
	sc.Step(`^resolving fails naming the build tag "([^"]*)"$`, s.resolvingFailsNamingTag)
	sc.Step(`^resolving fails asking for a subsystem to be enabled$`, s.resolvingAsksForASubsystem)
	sc.Step(`^the "([^"]*)" subsystem is restarted$`, s.subsystemRestarted)
	sc.Step(`^the "([^"]*)" subsystem keeps running$`, s.subsystemKeepsRunning)
	sc.Step(`^the "([^"]*)" subsystem is running$`, s.subsystemRunning)
	sc.Step(`^the "([^"]*)" subsystem is stopped$`, s.subsystemStopped)
	sc.Step(`^the "([^"]*)" subsystem is started from a configuration with port (\d+)$`, s.startedFromPort)
}

func TestServeSubsystemsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "serve-subsystems",
		ScenarioInitializer: initializeServeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/serve_subsystems.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("serve subsystems feature suite failed")
	}
}

// --- features/serve_dispatcher.feature ---

// dispatcherFeatureState drives the process dispatcher over a scripted worker.
// Real worker processes would turn the questions this feature is about - how long
// a restart waits, when that wait resets - into a matter of sleeping, so the
// worker is a function and the clock is fake.
type dispatcherFeatureState struct {
	home string

	// lives hands the next ending to whichever worker is running. A worker with
	// nothing waiting for it blocks, which is what a healthy one looks like from
	// here.
	lives chan workerRun

	mu       sync.Mutex
	attempts int
	waits    []time.Duration
	now      time.Time

	d      *Dispatcher
	cancel context.CancelFunc
	done   chan error

	record Record
	found  Record
	err    error
}

// workerRun is one scripted life of the worker: how long it lasted and how it
// ended.
type workerRun struct {
	uptime time.Duration
	code   int
}

func (s *dispatcherFeatureState) reset() {
	s.stop()
	s.home, _ = os.MkdirTemp("", "coddy-dispatch-*")
	s.lives = make(chan workerRun, 16)
	s.attempts = 0
	s.waits = nil
	s.now = time.Date(2026, 9, 10, 11, 26, 0, 0, time.UTC)
	s.err = nil
}

func (s *dispatcherFeatureState) stop() {
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
		s.cancel = nil
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

// worker runs until an ending is handed to it, then reports that ending.
func (s *dispatcherFeatureState) worker(ctx context.Context) (int, error) {
	s.mu.Lock()
	s.attempts++
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return 0, nil
	case run := <-s.lives:
		s.mu.Lock()
		s.now = s.now.Add(run.uptime)
		s.mu.Unlock()
		if run.code != 0 {
			return run.code, fmt.Errorf("worker exited with status %d", run.code)
		}
		return 0, nil
	}
}

func (s *dispatcherFeatureState) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *dispatcherFeatureState) recordWait(_ context.Context, d time.Duration) bool {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	s.mu.Unlock()
	return true
}

func (s *dispatcherFeatureState) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *dispatcherFeatureState) recordedWaits() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// --- Given ---

func (s *dispatcherFeatureState) supervisingAWorker() error {
	s.reset()
	s.d = &Dispatcher{
		Worker: s.worker,
		Policy: RestartPolicy{Min: time.Second, Max: 30 * time.Second, Steady: time.Minute},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Wait:   s.recordWait,
		Now:    s.clock,
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan error, 1)
	go func() { s.done <- s.d.Run(ctx) }()
	return waitFor(func() bool { return s.attemptCount() >= 1 })
}

func (s *dispatcherFeatureState) recordedItself() error {
	s.reset()
	s.record = Record{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		Identity:  ProcessStartedAt(os.Getpid()),
		Version:   "test",
		Config:    filepath.Join(s.home, "config.yaml"),
		Log:       filepath.Join(s.home, "logs", "serve.log"),
	}
	return WriteRecord(s.home, s.record)
}

// --- When ---

// endWorkers hands out the scripted endings and waits until the dispatcher has
// started the replacements, so a Then step reads a settled state.
func (s *dispatcherFeatureState) endWorkers(runs ...workerRun) error {
	before := s.attemptCount()
	for _, r := range runs {
		s.lives <- r
	}
	return waitFor(func() bool { return s.attemptCount() >= before+len(runs) })
}

func (s *dispatcherFeatureState) workerFails() error {
	return s.endWorkers(workerRun{code: 1})
}

func (s *dispatcherFeatureState) workerExitsCleanly() error {
	s.lives <- workerRun{code: 0}
	select {
	case s.err = <-s.done:
		s.cancel = nil
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the dispatcher kept running after a clean exit")
	}
}

func (s *dispatcherFeatureState) workerFailsRepeatedly(times int) error {
	runs := make([]workerRun, 0, times)
	for i := 0; i < times; i++ {
		runs = append(runs, workerRun{code: 1})
	}
	return s.endWorkers(runs...)
}

func (s *dispatcherFeatureState) workerStaysUpThenFails() error {
	return s.endWorkers(workerRun{uptime: 10 * time.Minute, code: 1})
}

func (s *dispatcherFeatureState) lookForARunningDispatcher() error {
	s.found, s.err = ReadRecord(s.home)
	return nil
}

// --- Then ---

func (s *dispatcherFeatureState) anotherWorkerStarted() error {
	if got := s.attemptCount(); got < 2 {
		return fmt.Errorf("the worker was started %d time(s), want a replacement", got)
	}
	return nil
}

func (s *dispatcherFeatureState) noFurtherWorkerStarted() error {
	if got := s.attemptCount(); got != 1 {
		return fmt.Errorf("the worker was started %d times, want exactly one", got)
	}
	if s.err != nil {
		return fmt.Errorf("the dispatcher reported %v for a clean exit", s.err)
	}
	return nil
}

func (s *dispatcherFeatureState) waitsGrow() error {
	waits := s.recordedWaits()
	if len(waits) < 2 {
		return fmt.Errorf("only %d wait(s) recorded, want several", len(waits))
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			return fmt.Errorf("wait %d (%s) did not grow past wait %d (%s): %v",
				i+1, waits[i], i, waits[i-1], waits)
		}
	}
	return nil
}

func (s *dispatcherFeatureState) stillSupervising() error {
	select {
	case err := <-s.done:
		return fmt.Errorf("the dispatcher gave up with %v", err)
	default:
	}
	if got := s.attemptCount(); got < 2 {
		return fmt.Errorf("the worker was started %d time(s), so nothing is being supervised", got)
	}
	return nil
}

func (s *dispatcherFeatureState) lastWaitIsTheShortest() error {
	waits := s.recordedWaits()
	if len(waits) == 0 {
		return fmt.Errorf("no waits recorded")
	}
	if last := waits[len(waits)-1]; last != s.d.Policy.Min {
		return fmt.Errorf("the wait after a healthy worker was %s, want the shortest (%s): %v",
			last, s.d.Policy.Min, waits)
	}
	return nil
}

func (s *dispatcherFeatureState) reportsTheDispatcher() error {
	if s.err != nil {
		return fmt.Errorf("looking for the dispatcher failed: %w", s.err)
	}
	if s.found.PID != s.record.PID {
		return fmt.Errorf("found pid %d, want %d", s.found.PID, s.record.PID)
	}
	if s.found.Config != s.record.Config {
		return fmt.Errorf("found config %q, want %q", s.found.Config, s.record.Config)
	}
	if !s.found.Running() {
		return fmt.Errorf("the recorded dispatcher is reported as gone, but it is this test process")
	}
	return nil
}

// --- runtime steps: a listener the running process cannot move ---

func (s *serveFeatureState) runningRuntimeUnderDispatcher() error {
	s.reset()
	s.restartable = true
	return s.launch()
}

func (s *serveFeatureState) runningRuntimeForeground() error {
	s.reset()
	s.restartable = false
	return s.launch()
}

func (s *serveFeatureState) bindAddressChanged() error {
	s.reload(func(c *config.Config) { c.HTTPServer.Port = 23456 })
	// A foreground runtime is supposed to do nothing here, so there is no edge
	// to wait for; give it a moment to have done the wrong thing.
	time.Sleep(200 * time.Millisecond)
	return nil
}

func (s *serveFeatureState) asksForARestart() error {
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the runtime kept running instead of asking for a restart")
	}
	s.cancel = nil
	if !errors.Is(s.runErr, ErrRestartRequested) {
		return fmt.Errorf("the runtime exited with %v, want a restart request", s.runErr)
	}
	return nil
}

func initializeDispatcherScenario(sc *godog.ScenarioContext) {
	d := &dispatcherFeatureState{}
	r := &serveFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		d.stop()
		r.stopSupervisor()
		return ctx, err
	})

	sc.Step(`^a dispatcher supervising a worker$`, d.supervisingAWorker)
	sc.Step(`^a dispatcher that recorded itself under the agent home$`, d.recordedItself)
	sc.Step(`^the worker fails$`, d.workerFails)
	sc.Step(`^the worker exits cleanly$`, d.workerExitsCleanly)
	sc.Step(`^the worker fails (\d+) times without staying up$`, d.workerFailsRepeatedly)
	sc.Step(`^a worker stays up before failing again$`, d.workerStaysUpThenFails)
	sc.Step(`^another coddy looks for a running dispatcher$`, d.lookForARunningDispatcher)
	sc.Step(`^the dispatcher starts another worker$`, d.anotherWorkerStarted)
	sc.Step(`^the dispatcher starts no further worker$`, d.noFurtherWorkerStarted)
	sc.Step(`^each restart waits longer than the one before it$`, d.waitsGrow)
	sc.Step(`^the dispatcher is still supervising$`, d.stillSupervising)
	sc.Step(`^the last restart waits the shortest time again$`, d.lastWaitIsTheShortest)
	sc.Step(`^it reports the dispatcher's process and the configuration it was started with$`, d.reportsTheDispatcher)

	sc.Step(`^a running runtime with the httpserver enabled under a dispatcher$`, r.runningRuntimeUnderDispatcher)
	sc.Step(`^a running runtime with the httpserver enabled in the foreground$`, r.runningRuntimeForeground)
	sc.Step(`^the HTTP bind address is changed through the configuration$`, r.bindAddressChanged)
	sc.Step(`^the runtime asks its dispatcher for a restart$`, r.asksForARestart)
	sc.Step(`^the "([^"]*)" subsystem keeps running$`, r.subsystemKeepsRunning)
}

func TestServeDispatcherFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "serve-dispatcher",
		ScenarioInitializer: initializeDispatcherScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/serve_dispatcher.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("serve dispatcher feature suite failed")
	}
}

// A process started as a bare relay opened no session store, and a surface
// that runs agent turns cannot start without one. A reload that enables such a
// surface there asks for a fresh process when something will start one, and
// otherwise says a restart is due - never starting the surface on nothing,
// which failed it and took the relay down with it.
func TestReloadEnablingASessionSurfaceInARelayOnlyProcess(t *testing.T) {
	for _, restartable := range []bool{false, true} {
		s := &serveFeatureState{}
		s.reset()
		s.restartable = restartable
		s.sessionless = true
		off := false
		s.cfg.HTTPServer.Enabled = &off
		s.cfg.Swarm.Enabled = true
		if err := s.launch(); err != nil {
			t.Fatal(err)
		}
		next := *s.cfg
		on := true
		next.HTTPServer.Enabled = &on
		s.reloads <- &next
		if restartable {
			select {
			case <-s.done:
				if !errors.Is(s.runErr, ErrRestartRequested) {
					t.Errorf("run ended with %v, want a restart request", s.runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a restartable relay-only process did not ask for a restart")
			}
		} else if err := s.settle(s.cfg); err != nil {
			t.Fatal(err)
		}
		if n := s.startCount(KindHTTP); n != 0 {
			t.Errorf("restartable=%v: the HTTP surface was started %d times without a session store", restartable, n)
		}
		s.stopSupervisor()
	}
}

// The fresh process a sessionless relay asks for has to be able to start: a
// reload that also enables a surface this binary was built without would make
// the replacement refuse its configuration at the pre-flight, again and
// again, with no relay serving. That reload is refused where it stands.
func TestSessionlessRelayDoesNotRestartIntoAConfigurationItCannotRun(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	s.restartable = true
	s.sessionless = true
	off := false
	s.cfg.HTTPServer.Enabled = &off
	s.cfg.Swarm.Enabled = true
	if err := s.launch(); err != nil {
		t.Fatal(err)
	}
	defer s.stopSupervisor()
	next := *s.cfg
	on := true
	next.HTTPServer.Enabled = &on
	next.Gateways.Telegram.Enabled = true
	s.reloads <- &next
	if err := s.settle(s.cfg); err != nil {
		t.Fatalf("a configuration its replacement cannot run: %v", err)
	}
	if n := s.startCount(KindSwarm); n != 1 {
		t.Errorf("the relay was started %d times, want it left running", n)
	}
}

// The same holds for a listener that moved: a restart into a configuration the
// fresh process's pre-flight refuses would take the relay down for good. The
// reload is refused and the relay keeps its address.
func TestListenerRestartIsNotRequestedIntoAConfigurationItCannotRun(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	for i := range s.subs {
		if s.subs[i].Kind == KindSwarm {
			s.subs[i].RestartKey = func(c *config.Config) string { return fmt.Sprint(c.Swarm.Port) }
		}
	}
	s.restartable = true
	s.sessionless = true
	off := false
	s.cfg.HTTPServer.Enabled = &off
	s.cfg.Swarm.Enabled = true
	s.cfg.Swarm.Port = 12346
	if err := s.launch(); err != nil {
		t.Fatal(err)
	}
	defer s.stopSupervisor()
	next := *s.cfg
	next.Swarm.Port = 12400
	next.Gateways.Telegram.Enabled = true
	s.reloads <- &next
	if err := s.settle(s.cfg); err != nil {
		t.Fatalf("a configuration its replacement cannot run: %v", err)
	}
}

// A change that needs a fresh process is weighed before anything moves. One
// the fresh process could not start on is refused as a whole: a surface the
// same change turned off keeps running, instead of being stopped on the way to
// the refusal, and the next change is read against the surfaces as they are.
func TestARefusedRestartLeavesEverySurfaceAsItWas(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	for i := range s.subs {
		if s.subs[i].Kind == KindSwarm {
			s.subs[i].RestartKey = func(c *config.Config) string { return fmt.Sprint(c.Swarm.Port) }
		}
	}
	s.restartable = true
	s.cfg.Swarm.Enabled = true
	s.cfg.Swarm.Port = 12346
	if err := s.launch(); err != nil {
		t.Fatal(err)
	}
	defer s.stopSupervisor()

	refused := *s.cfg
	off := false
	refused.HTTPServer.Enabled = &off
	refused.Gateways.Telegram.Enabled = true
	refused.Swarm.Port = 12400
	s.reloads <- &refused
	if err := s.settle(s.cfg); err != nil {
		t.Fatal(err)
	}
	if n := s.stopCount(KindHTTP); n != 0 {
		t.Errorf("the refused change stopped the HTTP surface (%d stops)", n)
	}
	if n := s.startCount(KindHTTP); n != 1 {
		t.Errorf("the HTTP surface was started %d times, want it left running (1)", n)
	}

	// The HTTP surface is still on the address it started with, so moving it
	// is a change for a fresh process, not one to start in place.
	moved := *s.cfg
	moved.HTTPServer.Port = 23456
	s.reloads <- &moved
	select {
	case <-s.done:
		s.cancel = nil
		if !errors.Is(s.runErr, ErrRestartRequested) {
			t.Errorf("run ended with %v, want a restart request", s.runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("moving the HTTP address did not ask for a fresh process")
	}
}
