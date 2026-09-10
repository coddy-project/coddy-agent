package serve

// Godog harness for features/serve_subsystems.feature: which surfaces one
// `coddy serve` process starts, what it refuses, and what a settings change
// rebuilds. The surfaces themselves are stand-ins, because the question here is
// the supervisor's, not any one server's.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

	sup     *Supervisor
	cancel  context.CancelFunc
	done    chan struct{}
	reloads chan *config.Config
}

func (s *serveFeatureState) reset() {
	s.stopSupervisor()
	s.cfg = &config.Config{}
	s.ready = nil
	s.err = nil
	s.starts = make(map[Kind]int)
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
	block := func(kind Kind) func(context.Context) error {
		return func(ctx context.Context) error {
			s.mu.Lock()
			s.starts[kind]++
			s.mu.Unlock()
			<-ctx.Done()
			return nil
		}
	}
	return []Subsystem{
		{
			Kind: KindHTTP, ConfigKey: "httpserver.enable", BuildTag: "http", Available: true,
			Enabled: func(c *config.Config) bool { return c.HTTPServer.IsEnabled() },
			Run:     block(KindHTTP),
		},
		{
			Kind: KindGateway, ConfigKey: "gateways.telegram.enable", BuildTag: "gateway", Available: gatewayAvailable,
			Enabled:     func(c *config.Config) bool { return c.Gateways.Telegram.Enabled },
			Fingerprint: func(c *config.Config) string { return c.Gateways.Telegram.Token },
			Run:         block(KindGateway),
		},
		{
			Kind: KindSwarm, ConfigKey: "swarm.enable", BuildTag: "swarm", Available: true,
			Enabled: func(c *config.Config) bool { return c.Swarm.Enabled },
			Run:     block(KindSwarm),
		},
		{
			Kind: KindScheduler, ConfigKey: "scheduler.enable", BuildTag: "scheduler", Available: true,
			Enabled:     func(c *config.Config) bool { return c.Scheduler.Enabled },
			Fingerprint: func(c *config.Config) string { return c.Scheduler.Dir },
			Run:         block(KindScheduler),
		},
	}
}

func (s *serveFeatureState) startCount(kind Kind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts[kind]
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
	go func() {
		defer close(s.done)
		_ = s.sup.Run(ctx, s.cfg, s.reloads)
	}()
	return waitFor(func() bool {
		return s.startCount(KindHTTP) == 1 && s.startCount(KindGateway) == 1
	})
}

// --- When ---

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

	sc.Step(`^the runtime resolves which subsystems to start$`, s.resolveSubsystems)
	sc.Step(`^the runtime resolves the httpserver listen address$`, s.resolveListenAddress)
	sc.Step(`^the telegram token is changed through the configuration$`, s.tokenChanged)
	sc.Step(`^the scheduler is enabled through the configuration$`, s.schedulerEnabled)
	sc.Step(`^the telegram gateway is disabled through the configuration$`, s.gatewayDisabled)

	sc.Step(`^the "([^"]*)" subsystem is enabled$`, s.subsystemEnabled)
	sc.Step(`^the "([^"]*)" subsystem is disabled$`, s.subsystemDisabled)
	sc.Step(`^the listen address is "([^"]*)"$`, s.listenAddressIs)
	sc.Step(`^resolving fails naming the build tag "([^"]*)"$`, s.resolvingFailsNamingTag)
	sc.Step(`^resolving fails asking for a subsystem to be enabled$`, s.resolvingAsksForASubsystem)
	sc.Step(`^the "([^"]*)" subsystem is restarted$`, s.subsystemRestarted)
	sc.Step(`^the "([^"]*)" subsystem keeps running$`, s.subsystemKeepsRunning)
	sc.Step(`^the "([^"]*)" subsystem is running$`, s.subsystemRunning)
	sc.Step(`^the "([^"]*)" subsystem is stopped$`, s.subsystemStopped)
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
