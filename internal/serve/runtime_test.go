package serve

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

type recordingBroker struct {
	requests []agent.DetachedPermissionRequest
}

func (b *recordingBroker) RequestDetachedPermission(_ context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.requests = append(b.requests, req)
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}

type decliningBroker struct{ err error }

func (b decliningBroker) RequestDetachedPermission(context.Context, agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	return nil, b.err
}

// waitingBroker shows the prompt and waits until the runtime withdraws it.
type waitingBroker struct {
	asked     chan struct{}
	withdrawn chan struct{}
}

func newWaitingBroker() *waitingBroker {
	return &waitingBroker{asked: make(chan struct{}, 1), withdrawn: make(chan struct{}, 1)}
}

func (b *waitingBroker) RequestDetachedPermission(ctx context.Context, _ agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	b.asked <- struct{}{}
	<-ctx.Done()
	b.withdrawn <- struct{}{}
	return nil, nil
}

var detachedReq = agent.DetachedPermissionRequest{ParentSessionID: "sess_parent", ChildSessionID: "sess_child", TaskID: "bg_1", AgentName: "writer"}

// Every turn on the shared manager is handed the runtime itself as its broker,
// long before a surface that can show a prompt is up - and after it went away.
// No surface at all is "nobody can be asked", which the relay turns into a
// refusal with a reason; an offered surface gets the request unchanged.
func TestRuntimeDetachedPermissionSurfaces(t *testing.T) {
	rt := &Runtime{}

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("no surface answered %v, want ErrNoDetachedApprover", err)
	}

	broker := &recordingBroker{}
	withdraw := rt.AddDetachedPermissionApprover(broker)
	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("offered surface answered %+v, %v; want its allow", res, err)
	}
	if len(broker.requests) != 1 || broker.requests[0].ChildSessionID != "sess_child" || broker.requests[0].TaskID != "bg_1" {
		t.Fatalf("surface saw %+v", broker.requests)
	}

	withdraw()
	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("withdrawn surface answered %v, want ErrNoDetachedApprover", err)
	}
}

// A surface restarted under a settings change can come up before the old one
// has finished stopping, so withdrawing the old offer must not take the new one
// with it.
func TestRuntimeWithdrawingAnOfferLeavesTheOthers(t *testing.T) {
	rt := &Runtime{}
	withdrawOld := rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	fresh := &recordingBroker{}
	rt.AddDetachedPermissionApprover(fresh)

	withdrawOld()
	withdrawOld()

	res, err := rt.RequestDetachedPermission(context.Background(), detachedReq)
	if err != nil || res == nil || res.OptionID != "allow" {
		t.Fatalf("after withdrawing the old offer got %+v, %v; want the fresh surface's allow", res, err)
	}
	if rt.AddDetachedPermissionApprover(nil) == nil {
		t.Fatal("offering nil must still return a callable withdraw")
	}
}

// When every surface says it cannot show this prompt - or failed to - nobody was
// asked, and the subagent must hear exactly that rather than "unanswered".
func TestRuntimeDetachedPermissionEverySurfaceDeclines(t *testing.T) {
	rt := &Runtime{}
	rt.AddDetachedPermissionApprover(decliningBroker{err: agent.ErrNoDetachedApprover})
	rt.AddDetachedPermissionApprover(decliningBroker{err: errors.New("telegram: send failed")})

	if _, err := rt.RequestDetachedPermission(context.Background(), detachedReq); !errors.Is(err, agent.ErrNoDetachedApprover) {
		t.Fatalf("every surface declined but got %v, want ErrNoDetachedApprover", err)
	}
}

// The wait ends with the run: a stopped or timed-out child cancels the context,
// and every surface that shows the prompt must be told so it can take it down.
func TestRuntimeDetachedPermissionCancelReachesEverySurface(t *testing.T) {
	rt := &Runtime{}
	web, chat := newWaitingBroker(), newWaitingBroker()
	rt.AddDetachedPermissionApprover(web)
	rt.AddDetachedPermissionApprover(chat)

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res *acp.PermissionResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := rt.RequestDetachedPermission(ctx, detachedReq)
		done <- outcome{res, err}
	}()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.asked:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface was never asked")
		}
	}
	cancel()
	for _, b := range []*waitingBroker{web, chat} {
		select {
		case <-b.withdrawn:
		case <-time.After(5 * time.Second):
			t.Fatal("a surface kept the prompt after the run ended")
		}
	}
	select {
	case out := <-done:
		if out.res != nil || out.err != nil {
			t.Fatalf("cancelled wait answered %+v, %v; want nil, nil", out.res, out.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime never returned after the run ended")
	}
}

// A relay alone opens no session store, so there is no manager to hold the live
// configuration. The runtime holds it instead, and a reload goes through the
// same three questions a manager answers - what is live, replace it, who is
// watching - so a relay's settings take effect the way an agent's do (issue
// #401).
func TestRuntimeWithoutAManagerHoldsTheLiveConfiguration(t *testing.T) {
	first := &config.Config{}
	first.Swarm.Name = "office"
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: first}); err != nil {
		t.Fatal(err)
	}
	if rt.Mgr != nil {
		t.Fatal("a process that runs no turns opened a session manager")
	}
	if rt.Cfg() != first {
		t.Fatal("the runtime does not serve the configuration it started with")
	}
	var seen []*config.Config
	remove := rt.AddConfigObserver(func(c *config.Config) { seen = append(seen, c) })
	next := &config.Config{}
	next.Swarm.Name = "office-2"
	rt.ReplaceConfig(next)
	if rt.Cfg() != next || len(seen) != 1 || seen[0] != next {
		t.Fatalf("replace: live=%p seen=%v", rt.Cfg(), seen)
	}
	remove()
	rt.ReplaceConfig(first)
	if len(seen) != 1 {
		t.Fatal("a removed observer still hears replacements")
	}
	rt.ReplaceConfig(nil)
	if rt.Cfg() != first {
		t.Fatal("a nil configuration replaced the live one")
	}
}

func TestRuntimePassesProcessOverridesToItsManager(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	rt := &Runtime{}
	if err := rt.Init(Options{
		Cfg: cfg, NeedsSessions: true, SessionsRoot: t.TempDir(),
		AdjustConfig: func(next *config.Config) error {
			next.MCP.ProjectTrust = config.ProjectTrustDeny
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	next := &config.Config{}
	if err := rt.Mgr.InstallConfig(next); err != nil {
		t.Fatal(err)
	}
	if rt.Cfg() != next || rt.Cfg().MCP.ProjectTrust != config.ProjectTrustDeny {
		t.Fatalf("runtime installed configuration without process overrides: %+v", rt.Cfg().MCP)
	}
}

// A relay alone holds its configuration in the runtime, and two overlapping
// replacements reach the supervisor in the order they were stored, as they do
// through a manager: the other order left it rebuilding the relay on the older
// one.
func TestRuntimeReplacementsReachObserversInTheOrderTheyWereStored(t *testing.T) {
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: &config.Config{}}); err != nil {
		t.Fatal(err)
	}
	first, second := &config.Config{}, &config.Config{}
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var last *config.Config
	remove := rt.AddConfigObserver(func(c *config.Config) {
		if c == first {
			close(entered)
			<-release
		}
		mu.Lock()
		last = c
		mu.Unlock()
	})
	defer remove()
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { rt.ReplaceConfig(first); close(firstDone) }()
	<-entered
	go func() { rt.ReplaceConfig(second); close(secondDone) }()
	// Give a second replacement that is not held back the time to publish
	// before the first one's publication resumes.
	select {
	case <-secondDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a replacement never finished")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if last != rt.Cfg() {
		t.Fatalf("the observer was left on %p while the runtime holds %p", last, rt.Cfg())
	}
}

// TestRuntimeConnectsANewSessionsServersInTheBackground: a session coddy serve
// opens - the first message of a web chat, a messenger chat - returns before
// its MCP servers answer, and its first turn says it was taken before it waits
// for them (issue #357). A cold stdio server held the first frame of a chat for
// sixteen seconds.
func TestRuntimeConnectsANewSessionsServersInTheBackground(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: cfg, NeedsSessions: true, SessionsRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if rt.Mgr == nil {
		t.Fatal("a process that runs turns opened no session manager")
	}
	if !rt.Mgr.NewSessionsBackgroundMCP() {
		t.Fatal("coddy serve connects a new session's MCP servers before session/new returns")
	}
	if rt.Mgr.BackgroundMCPConnect() {
		t.Fatal("coddy serve starts a stored session's MCP servers when it is loaded to be read")
	}
}

// launched starts a supervisor over the harness descriptors and waits until it
// has applied the configuration it started with, the point from which
// Status(that configuration) reads "applied".
func launched(t *testing.T, s *serveFeatureState) {
	t.Helper()
	if err := s.launch(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stopSupervisor)
	if err := waitFor(func() bool {
		s.sup.mu.Lock()
		defer s.sup.mu.Unlock()
		return s.sup.applied == s.cfg
	}); err != nil {
		t.Fatalf("the supervisor never applied its starting configuration: %v", err)
	}
}

func statusRow(t *testing.T, rows []SubsystemStatus, kind Kind) SubsystemStatus {
	t.Helper()
	for _, row := range rows {
		if row.Kind == kind {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", kind, rows)
	return SubsystemStatus{}
}

// A listener's new address is pending as soon as the configuration says so:
// a read right after a save sees it before the supervisor has been handed the
// configuration, and moving the address back clears it. Whether anyone has to
// restart the process depends on a dispatcher being behind it.
func TestSupervisorStatusPendingBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name         string
		restartable  bool
		wantRequired bool
	}{
		{"a foreground process waits for a manual restart", false, true},
		{"a process under a dispatcher restarts by itself", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &serveFeatureState{}
			s.reset()
			s.restartable = tc.restartable
			launched(t, s)

			moved := *s.cfg
			moved.HTTPServer.Port = 23456
			rows, required, _ := s.sup.Status(&moved)
			if row := statusRow(t, rows, KindHTTP); row.State != StateRunning {
				t.Errorf("http row = %+v, want running until the restart", row)
			}
			if required != tc.wantRequired {
				t.Errorf("restartRequired = %v, want %v", required, tc.wantRequired)
			}

			if _, required, _ = s.sup.Status(s.cfg); required {
				t.Errorf("moving the address back left restartRequired = %v", required)
			}
		})
	}
}

// A dispatcher restarts the process only into a configuration its pre-flight
// accepts. When it would not, the change waits for a manual restart.
func TestSupervisorStatusRestartIntoARefusedConfiguration(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	s.restartable = true
	launched(t, s)

	unrunnable := *s.cfg
	unrunnable.Gateways.Telegram.Enabled = true
	unrunnable.HTTPServer.Port = 23456
	if _, required, _ := s.sup.Status(&unrunnable); !required {
		t.Error("a dispatcher that would not restart into the configuration still reads as restarting by itself")
	}

	// With no address to move there is nothing to restart for, runnable or not.
	unrunnable.HTTPServer.Port = 0
	if _, required, _ := s.sup.Status(&unrunnable); required {
		t.Error("without a pending address a restart is required")
	}

	// Without a dispatcher the change just waits, whatever the pre-flight says.
	s.sup.Restartable = false
	unrunnable.HTTPServer.Port = 23456
	if _, required, _ := s.sup.Status(&unrunnable); !required {
		t.Error("foreground: no manual restart is required")
	}
}

// A process started without a session store cannot start a surface that runs
// turns in place (applyConfig refuses it the same way): the saved change waits
// for a restart, like a moved listener.
func TestSupervisorStatusSessionlessProcessNeedsRestartForASessionSurface(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.restartable = false
	launched(t, s)
	s.sup.mu.Lock()
	s.sup.Sessionless = true
	s.sup.mu.Unlock()

	next := *s.cfg
	next.Scheduler.Enabled = true
	rows, required, _ := s.sup.Status(&next)
	if !required {
		t.Errorf("a session surface a sessionless process cannot start reads restartRequired = false (rows %+v)", rows)
	}
	if _, required, _ := s.sup.Status(s.cfg); required {
		t.Error("the configuration the process runs on requires a restart")
	}
}

// Before the supervisor has applied a configuration every surface it enables
// is starting, never stopped; once it has, an enabled one that is not running
// is stopped.
func TestSupervisorStatusStartingBeforeLaunch(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.cfg.Gateways.Telegram.Enabled = true
	idle := NewSupervisor(nil, s.subs)
	rows, required, _ := idle.Status(s.cfg)
	if len(rows) != 2 || required {
		t.Fatalf("an unstarted supervisor reads %+v, restartRequired = %v; want the two enabled surfaces", rows, required)
	}
	for i, kind := range []Kind{KindHTTP, KindGateway} {
		if rows[i].Kind != kind || rows[i].State != StateStarting {
			t.Errorf("row %d = %+v, want %s starting, in declaration order", i, rows[i], kind)
		}
	}

	s.cfg.Gateways.Telegram.Enabled = false
	launched(t, s)
	wider := *s.cfg
	wider.Scheduler.Enabled = true
	rows, _, _ = s.sup.Status(&wider)
	if got := statusRow(t, rows, KindScheduler).State; got != StateStarting {
		t.Errorf("a surface the configuration enabled, before the supervisor saw it: %q, want starting", got)
	}
	s.reloads <- &wider
	if err := s.settle(&wider); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.sup.Status(&wider)
	if got := statusRow(t, rows, KindScheduler).State; got != StateRunning {
		t.Errorf("after the supervisor applied it: %q, want running", got)
	}
}

// An enabled surface the supervisor applied and did not start (this binary
// has no support for it) is stopped.
func TestSupervisorStatusStoppedWhenAppliedButNotRunning(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false)
	launched(t, s)

	next := *s.cfg
	next.Gateways.Telegram.Enabled = true
	s.reloads <- &next
	if err := s.settle(&next); err != nil {
		t.Fatal(err)
	}
	rows, required, _ := s.sup.Status(&next)
	if row := statusRow(t, rows, KindGateway); row.State != StateStopped {
		t.Errorf("gateway row = %+v, want stopped", row)
	}
	if required {
		t.Error("restartRequired for a surface this binary cannot run")
	}
}

// A listener the configuration turns off is retained until the supervisor
// stops it, and it may simply stop: a new address for it is no reason to
// restart anything.
func TestSupervisorStatusDisabledListenerHasNoPending(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.restartable = false
	launched(t, s)

	off := false
	disabled := *s.cfg
	disabled.HTTPServer.Enabled = &off
	disabled.HTTPServer.Port = 23456
	rows, required, _ := s.sup.Status(&disabled)
	if row := statusRow(t, rows, KindHTTP); row.State != StateRetained {
		t.Errorf("http row = %+v, want retained", row)
	}
	if required {
		t.Error("restartRequired for a listener that may simply stop")
	}
}

// An auxiliary subsystem that fails - an exporter rebuilt onto a port that is
// taken - stops alone: the surfaces keep running, the row reads stopped, and
// the next configuration that enables it starts it again.
func TestSupervisorAnAuxiliaryFailureStopsOnlyItself(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	for i := range s.subs {
		if s.subs[i].Kind != KindMetrics {
			continue
		}
		run := s.subs[i].Run
		s.subs[i].Fingerprint = func(c *config.Config) string { return strconv.Itoa(c.Metrics.Port) }
		s.subs[i].Run = func(ctx context.Context, c *config.Config) error {
			if c.Metrics.Port == 1 {
				return errors.New("listen tcp :1: bind: address already in use")
			}
			return run(ctx, c)
		}
	}
	s.cfg.Metrics.Enable = true
	launched(t, s)

	taken := *s.cfg
	taken.Metrics.Port = 1
	if err := s.settle(&taken); err != nil {
		t.Fatalf("the process ended with the exporter: %v", err)
	}
	if err := waitFor(func() bool {
		rows, _, _ := s.sup.Status(&taken)
		return statusRow(t, rows, KindMetrics).State == StateStopped
	}); err != nil {
		t.Fatalf("the failed exporter never read as stopped: %v", err)
	}
	rows, _, _ := s.sup.Status(&taken)
	if got := statusRow(t, rows, KindHTTP).State; got != StateRunning {
		t.Errorf("http after the exporter failed: %q, want running", got)
	}

	free := *s.cfg
	free.Metrics.Port = 2
	if err := s.settle(&free); err != nil {
		t.Fatal(err)
	}
	if err := waitFor(func() bool {
		rows, _, _ := s.sup.Status(&free)
		return statusRow(t, rows, KindMetrics).State == StateRunning
	}); err != nil {
		t.Errorf("the exporter on a free port never ran again: %v", err)
	}
}

// freeLoopbackPort returns a port nothing listens on at the moment.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// A running instance reads the address it was started on. A listener with a
// restart key keeps it, and the address the live configuration moved it to is
// pending until the process is replaced; one that has no restart key and is
// rebuilt in place reads the new address once it runs there, with nothing
// pending and no restart, and none when the rebuild failed and the instance
// is gone.
func TestSupervisorStatusAddresses(t *testing.T) {
	t.Run("a listener that needs a fresh process", func(t *testing.T) {
		s := &serveFeatureState{}
		s.reset()
		s.restartable = false
		launched(t, s)

		rows, required, _ := s.sup.Status(s.cfg)
		if row := statusRow(t, rows, KindHTTP); row.Address != "12345" || row.PendingAddress != "" || required {
			t.Fatalf("http row = %+v, restartRequired = %v; want running on 12345, nothing pending", row, required)
		}

		moved := *s.cfg
		moved.HTTPServer.Port = 23456
		rows, required, _ = s.sup.Status(&moved)
		row := statusRow(t, rows, KindHTTP)
		if row.State != StateRunning || row.Address != "12345" || row.PendingAddress != "23456" || !required {
			t.Errorf("http row = %+v, restartRequired = %v; want running on 12345 with 23456 pending", row, required)
		}

		rows, required, _ = s.sup.Status(s.cfg)
		if row := statusRow(t, rows, KindHTTP); row.PendingAddress != "" || required {
			t.Errorf("moving the address back left %+v, restartRequired = %v", row, required)
		}

		// A listener the configuration turns off may simply stop: it keeps the
		// address it runs on and has nothing pending.
		off := false
		disabled := moved
		disabled.HTTPServer.Enabled = &off
		rows, _, _ = s.sup.Status(&disabled)
		if row := statusRow(t, rows, KindHTTP); row.State != StateRetained || row.Address != "12345" || row.PendingAddress != "" {
			t.Errorf("a disabled listener reads %+v, want retained on 12345 with nothing pending", row)
		}
	})

	t.Run("a listener rebuilt in place", func(t *testing.T) {
		s := &serveFeatureState{}
		s.reset()
		for i := range s.subs {
			if s.subs[i].Kind != KindMetrics {
				continue
			}
			run := s.subs[i].Run
			s.subs[i].Address = func(c *config.Config) string { return c.Metrics.ListenAddr() }
			s.subs[i].Fingerprint = func(c *config.Config) string { return c.Metrics.ListenAddr() }
			s.subs[i].Run = func(ctx context.Context, c *config.Config) error {
				ln, err := net.Listen("tcp", c.Metrics.ListenAddr())
				if err != nil {
					return err
				}
				defer func() { _ = ln.Close() }()
				return run(ctx, c)
			}
		}
		s.cfg.Metrics.Enable = true
		s.cfg.Metrics.Host = "127.0.0.1"
		s.cfg.Metrics.Port = freeLoopbackPort(t)
		launched(t, s)

		row := func(cfg *config.Config) (SubsystemStatus, bool) {
			rows, required, _ := s.sup.Status(cfg)
			return statusRow(t, rows, KindMetrics), required
		}
		first := s.cfg.Metrics.ListenAddr()
		if r, required := row(s.cfg); r.State != StateRunning || r.Address != first || r.PendingAddress != "" || required {
			t.Fatalf("metrics row = %+v, restartRequired = %v; want running on %s, nothing pending", r, required, first)
		}

		moved := *s.cfg
		moved.Metrics.Port = freeLoopbackPort(t)
		second := moved.Metrics.ListenAddr()
		if err := s.settle(&moved); err != nil {
			t.Fatal(err)
		}
		if err := waitFor(func() bool {
			r, _ := row(&moved)
			return r.State == StateRunning && r.Address == second
		}); err != nil {
			r, _ := row(&moved)
			t.Fatalf("after the rebuild the row is %+v, want running on %s: %v", r, second, err)
		}
		if r, required := row(&moved); r.PendingAddress != "" || required {
			t.Errorf("a rebuild in place left %+v, restartRequired = %v", r, required)
		}

		// The address a rebuild is moving to is taken: the instance fails alone.
		holder, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = holder.Close() }()
		taken := *s.cfg
		taken.Metrics.Port = holder.Addr().(*net.TCPAddr).Port
		if err := s.settle(&taken); err != nil {
			t.Fatalf("the process ended with the exporter: %v", err)
		}
		if err := waitFor(func() bool {
			r, _ := row(&taken)
			return r.State == StateStopped
		}); err != nil {
			t.Fatalf("the exporter on a taken address never read as stopped: %v", err)
		}
		if r, required := row(&taken); r.Address != "" || r.PendingAddress != "" || required {
			t.Errorf("failed row = %+v, restartRequired = %v; want stopped with no address", r, required)
		}
	})
}

// A dispatcher restarts the process only into a configuration its pre-flight
// accepts. When it would not, the read carries the pre-flight's own message,
// and only then.
func TestSupervisorStatusRefusal(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.subs = s.describe(false) // no gateway in this build
	s.restartable = true
	launched(t, s)

	unrunnable := *s.cfg
	unrunnable.Gateways.Telegram.Enabled = true
	unrunnable.HTTPServer.Port = 23456
	_, preflight := Resolve(&unrunnable, s.subs)
	if preflight == nil {
		t.Fatal("the pre-flight accepts a configuration that enables a surface the build lacks")
	}
	_, required, refused := s.sup.Status(&unrunnable)
	if !required || refused != preflight.Error() ||
		!strings.Contains(refused, "gateways.telegram.enable") || !strings.Contains(refused, "-tags gateway") {
		t.Errorf("restartRequired = %v, refused = %q; want the pre-flight's message %q", required, refused, preflight)
	}

	// A change the dispatcher would restart into reads no refusal.
	runnable := *s.cfg
	runnable.HTTPServer.Port = 23456
	if _, required, refused := s.sup.Status(&runnable); required || refused != "" {
		t.Errorf("a restartable change: restartRequired = %v, refused = %q; want neither", required, refused)
	}

	// With no address to move there is nothing to restart for, runnable or not.
	unrunnable.HTTPServer.Port = 0
	if _, required, refused := s.sup.Status(&unrunnable); required || refused != "" {
		t.Errorf("without a pending address: restartRequired = %v, refused = %q", required, refused)
	}

	// Without a dispatcher the change waits for a manual restart whatever the
	// pre-flight says, so there is nothing to refuse.
	s.sup.Restartable = false
	unrunnable.HTTPServer.Port = 23456
	if _, required, refused := s.sup.Status(&unrunnable); !required || refused != "" {
		t.Errorf("foreground: restartRequired = %v, refused = %q; want a manual restart and no refusal", required, refused)
	}
}

// heldTask is background work that runs until the test lets it go.
type heldTask struct {
	release chan struct{}
	once    sync.Once
}

func (h *heldTask) finish()                     { h.once.Do(func() { close(h.release) }) }
func (h *heldTask) Wait() (int, error)          { <-h.release; return 0, nil }
func (h *heldTask) Stop(time.Duration) error    { h.finish(); return nil }
func (h *heldTask) PID() int                    { return 0 }
func (h *heldTask) ProcessStartedAt() time.Time { return time.Time{} }

// The snapshot is what the process reads off itself, piece by piece: what it
// is, the supervisor's reading of the live configuration, the turns and tasks
// in flight, and the disk of its session store. A piece the process has no
// source for is absent, never zero.
func TestRuntimeStatusSnapshot(t *testing.T) {
	t.Run("a runtime with nothing attached says what it is", func(t *testing.T) {
		rt := &Runtime{}
		snap := rt.Status()
		if snap.Build.Version != version.Get() || snap.Build.GoVersion != runtime.Version() ||
			snap.Build.GOOS != runtime.GOOS || snap.Build.GOARCH != runtime.GOARCH {
			t.Errorf("build = %+v", snap.Build)
		}
		if snap.Subsystems != nil || snap.RestartRequired || snap.RestartRefused != "" ||
			snap.TurnsRunning != 0 || !snap.OldestTurnStartedAt.IsZero() || snap.Disk != nil || snap.Unavailable != nil {
			t.Errorf("nothing is attached, yet the snapshot says %+v", snap)
		}
	})

	t.Run("a runtime with a manager and no turn reports none", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Paths.Home = t.TempDir()
		rt := &Runtime{}
		if err := rt.Init(Options{Cfg: cfg, NeedsSessions: true, SessionsRoot: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if snap := rt.Status(); snap.TurnsRunning != 0 || !snap.OldestTurnStartedAt.IsZero() {
			t.Errorf("turns = %d, oldest = %v; want none", snap.TurnsRunning, snap.OldestTurnStartedAt)
		}
	})

	t.Run("the supervisor reads the live configuration", func(t *testing.T) {
		s := &serveFeatureState{}
		s.reset()
		s.subs = s.describe(false) // no gateway in this build
		s.restartable = true
		launched(t, s)
		rt := &Runtime{}
		if err := rt.Init(Options{Cfg: s.cfg}); err != nil {
			t.Fatal(err)
		}
		rt.SetSupervisor(s.sup)

		unrunnable := *s.cfg
		unrunnable.Gateways.Telegram.Enabled = true
		unrunnable.HTTPServer.Port = 23456
		rt.ReplaceConfig(&unrunnable)
		snap := rt.Status()
		want := []SubsystemStatus{
			{Kind: KindHTTP, State: StateRunning, Address: "12345", PendingAddress: "23456"},
			{Kind: KindGateway, State: StateStarting},
		}
		if !reflect.DeepEqual(snap.Subsystems, want) {
			t.Errorf("subsystems = %+v, want %+v", snap.Subsystems, want)
		}
		if !snap.RestartRequired || !strings.Contains(snap.RestartRefused, "gateways.telegram.enable") {
			t.Errorf("restartRequired = %v, refused = %q; want a restart the dispatcher would not make, and why", snap.RestartRequired, snap.RestartRefused)
		}
	})

	t.Run("background work in flight is counted by kind and status", func(t *testing.T) {
		held := &heldTask{release: make(chan struct{})}
		launched, err := bgtask.Default().Launch(
			bgtask.Spec{SessionID: "sess_status", Kind: bgtask.KindAgent, Label: "explore", Agent: &bgtask.AgentInfo{Name: "explore"}},
			func(string, io.Writer) (bgtask.Handle, error) { return held, nil })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			held.finish()
			_, _ = bgtask.Default().Wait(context.Background(), "sess_status", launched.ID, 2*time.Second)
		})
		if got := (&Runtime{}).Status().Tasks["agent"]["running"]; got != 1 {
			t.Errorf("running agent tasks = %d, want 1", got)
		}
	})
}

// The disk of the session store is one reading of its folder per snapshot,
// through the seam a test lowers the figures with. A folder that cannot be
// read, and a process with no store, have no disk rather than a full one.
func TestRuntimeStatusDisk(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store bool
		free  func(string) (uint64, uint64, error)
		want  *Disk
	}{
		{"low", true, func(string) (uint64, uint64, error) { return 400 << 20, 1 << 30, nil },
			&Disk{AvailableBytes: 400 << 20, SizeBytes: 1 << 30}},
		{"unreadable", true, func(string) (uint64, uint64, error) { return 0, 0, errors.New("permission denied") }, nil},
		{"no store", false, func(string) (uint64, uint64, error) { panic("read the disk of no store") }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &Runtime{diskFree: tc.free}
			if tc.store {
				rt.Store = &session.FileStore{Root: t.TempDir()}
			}
			if got := rt.Status().Disk; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("disk = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("the folder of the store is what is read", func(t *testing.T) {
		var asked string
		rt := &Runtime{Store: &session.FileStore{Root: t.TempDir()}, diskFree: func(path string) (uint64, uint64, error) {
			asked = path
			return 1, 2, nil
		}}
		rt.Status()
		if asked != rt.Store.Root {
			t.Errorf("read %q, want the sessions folder %q", asked, rt.Store.Root)
		}
	})

	t.Run("the platform reads it by default", func(t *testing.T) {
		rt := &Runtime{Store: &session.FileStore{Root: t.TempDir()}}
		disk := rt.Status().Disk
		if disk == nil {
			t.Skip("no free-space call on this platform")
		}
		if disk.SizeBytes == 0 || disk.AvailableBytes > disk.SizeBytes {
			t.Errorf("disk = %+v", disk)
		}
	})
}

// The process says how it was started from the role the environment named
// when the runtime was initialised, in a process that opens a session store
// and in one that does not. coddy serve clears the variable for the processes
// it starts right after, so a status read later still has the answer.
func TestRuntimeStatusProcess(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		want       StartedBy
	}{
		{"the systemd unit", RoleService, StartedBySystemd},
		{"the worker of a daemon", RoleWorker, StartedByDaemon},
		{"no role", "", StartedByForeground},
		{"the dispatcher, which serves nothing", RoleDispatcher, StartedByForeground},
		{"a value nothing sets", "supervisor", StartedByForeground},
	} {
		for _, sessions := range []bool{false, true} {
			t.Run(tc.name+" sessions="+strconv.FormatBool(sessions), func(t *testing.T) {
				t.Setenv(EnvRole, tc.role)
				home := t.TempDir()
				cfg := &config.Config{}
				cfg.Paths.Home = home
				rt := &Runtime{}
				before := time.Now()
				err := rt.Init(Options{
					Cfg: cfg, NeedsSessions: sessions, SessionsRoot: t.TempDir(),
					CLI: config.CLIPaths{Home: home, CWD: t.TempDir(), Config: filepath.Join(home, "elsewhere.yaml")},
				})
				after := time.Now()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Unsetenv(EnvRole); err != nil {
					t.Fatal(err)
				}

				snap := rt.Status()
				if snap.StartedBy != tc.want {
					t.Errorf("startedBy = %q, want %q", snap.StartedBy, tc.want)
				}
				if snap.ConfigFile != rt.Paths.ConfigPath || snap.ConfigFile != filepath.Join(home, "elsewhere.yaml") {
					t.Errorf("configFile = %q, want the config path %q", snap.ConfigFile, rt.Paths.ConfigPath)
				}
				if snap.StartedAt.Before(before) || snap.StartedAt.After(after) {
					t.Errorf("startedAt = %v, want when Init ran, between %v and %v", snap.StartedAt, before, after)
				}
				if again := rt.Status(); !again.StartedAt.Equal(snap.StartedAt) {
					t.Errorf("a second read gave %v, the first gave %v", again.StartedAt, snap.StartedAt)
				}
			})
		}
	}
}

// A disk that cannot be read is named as unavailable, whatever the reason; a
// process with no store has no disk to name, and a good reading names nothing.
func TestRuntimeStatusDiskUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store bool
		free  func(string) (uint64, uint64, error)
		disk  *Disk
		want  []string
	}{
		{"a failed read", true, func(string) (uint64, uint64, error) { return 0, 0, errors.New("permission denied") }, nil, []string{"disk"}},
		{"a platform without the call", true, func(string) (uint64, uint64, error) { return 0, 0, platform.ErrDiskFreeUnsupported }, nil, []string{"disk"}},
		{"a good read", true, func(string) (uint64, uint64, error) { return 1 << 20, 1 << 30, nil }, &Disk{AvailableBytes: 1 << 20, SizeBytes: 1 << 30}, nil},
		{"no store", false, func(string) (uint64, uint64, error) { panic("read the disk of no store") }, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &Runtime{diskFree: tc.free}
			if tc.store {
				rt.Store = &session.FileStore{Root: t.TempDir()}
			}
			snap := rt.Status()
			if !reflect.DeepEqual(snap.Disk, tc.disk) || !reflect.DeepEqual(snap.Unavailable, tc.want) {
				t.Errorf("disk = %+v, unavailable = %v; want %+v and %v", snap.Disk, snap.Unavailable, tc.disk, tc.want)
			}
		})
	}
}

// The exporter's row says whether a scrape has to carry a token, read from the
// live configuration, and no other row says anything of the kind.
func TestRuntimeStatusExporterCredential(t *testing.T) {
	s := &serveFeatureState{}
	s.reset()
	s.cfg.Metrics.Enable = true
	s.cfg.Gateways.Telegram.Enabled = true
	s.cfg.Scheduler.Enabled = true
	launched(t, s)
	rt := &Runtime{}
	if err := rt.Init(Options{Cfg: s.cfg}); err != nil {
		t.Fatal(err)
	}
	rt.SetSupervisor(s.sup)

	for _, tc := range []struct {
		token string
		want  Credential
	}{
		{"s3cret-scrape-token", CredentialToken},
		{"", CredentialNone},
	} {
		next := *s.cfg
		next.Metrics.Token = tc.token
		rt.ReplaceConfig(&next)
		rows := rt.Status().Subsystems
		if len(rows) < 4 {
			t.Fatalf("rows = %+v, want the http, gateway, scheduler and metrics rows", rows)
		}
		for _, row := range rows {
			var want Credential
			if row.Kind == KindMetrics {
				want = tc.want
			}
			if row.Credential != want {
				t.Errorf("token %q: %s row credential = %q, want %q", tc.token, row.Kind, row.Credential, want)
			}
			if tc.token != "" && strings.Contains(string(row.Credential), tc.token) {
				t.Errorf("the %s row carries the token", row.Kind)
			}
		}
	}
}
