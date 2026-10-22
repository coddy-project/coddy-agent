//go:build cli

package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestConsoleStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	saveConsoleState(home, consoleState{LastModel: "zed/m"})
	got := loadConsoleState(home)
	if got.LastModel != "zed/m" {
		t.Fatalf("LastModel = %q, want %q", got.LastModel, "zed/m")
	}
}

func TestLoadConsoleStateMissingFile(t *testing.T) {
	got := loadConsoleState(t.TempDir())
	if got.LastModel != "" {
		t.Fatalf("LastModel = %q, want empty for a missing file", got.LastModel)
	}
}

func TestLoadConsoleStateCorruptFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(consoleStatePath(home), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadConsoleState(home)
	if got.LastModel != "" {
		t.Fatalf("LastModel = %q, want empty for a corrupt file", got.LastModel)
	}
}

func TestSaveConsoleStateEmptyHome(t *testing.T) {
	// Must not panic or write anywhere.
	saveConsoleState("", consoleState{LastModel: "x"})
	if _, err := os.Stat(consoleStatePath("")); err == nil {
		t.Fatal("a state file appeared for an empty home")
	}
}

func TestSaveConsoleStatePreservesUnknownFieldsLater(t *testing.T) {
	home := t.TempDir()
	saveConsoleState(home, consoleState{LastModel: "a/m"})
	if _, err := os.Stat(filepath.Join(home, "console-state.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestPickInitialModel(t *testing.T) {
	cases := []struct {
		name    string
		stored  string
		offered []string
		want    string
	}{
		{name: "stored wins", stored: "zed/m", offered: []string{"aaa/m", "zed/m"}, want: "zed/m"},
		{name: "no stored picks alphabetical", stored: "", offered: []string{"zed/m", "aaa/m"}, want: "aaa/m"},
		{name: "stored gone picks alphabetical", stored: "removed/m", offered: []string{"zed/m", "aaa/m"}, want: "aaa/m"},
		{name: "empty offered", stored: "zed/m", offered: nil, want: ""},
		{name: "blank ids skipped", stored: "", offered: []string{"  ", "zed/m"}, want: "zed/m"},
		{name: "stored wins only when offered", stored: "zed/m", offered: []string{"other/m"}, want: "other/m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickInitialModel(tc.stored, tc.offered); got != tc.want {
				t.Fatalf("pickInitialModel(%q, %v) = %q, want %q", tc.stored, tc.offered, got, tc.want)
			}
		})
	}
}

// modelWriteTrace sits in front of the manager and records the writes of a
// model into a session in the order they land. It can hold the write of one
// model back until the test lets it go: that is how a test forces the order
// the lost race of issue #542 needed, the remembered model's write reaching the
// manager after everything the startup does without waiting for it. The hold
// is a channel, not a delay, so the order does not depend on how long the rest
// of the startup takes.
type modelWriteTrace struct {
	backend
	held string

	entered   chan struct{}
	enterOnce sync.Once
	release   chan struct{}
	letOnce   sync.Once

	mu     sync.Mutex
	landed []string
}

// newModelWriteTrace holds back the write of held, until letGo. The hold is
// also let go when the test ends, so a failed test leaves no write parked.
func newModelWriteTrace(t *testing.T, held string) *modelWriteTrace {
	m := &modelWriteTrace{held: held, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(m.letGo)
	return m
}

// around puts the trace in front of b: the wrap newModelStartupApp takes.
func (m *modelWriteTrace) around(b backend) backend {
	m.backend = b
	return m
}

func (m *modelWriteTrace) HandleSessionSetConfigOption(ctx context.Context, p acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if p.ConfigID != "model" {
		return m.backend.HandleSessionSetConfigOption(ctx, p)
	}
	if p.Value == m.held {
		m.enterOnce.Do(func() { close(m.entered) })
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	res, err := m.backend.HandleSessionSetConfigOption(ctx, p)
	if err == nil {
		m.mu.Lock()
		m.landed = append(m.landed, p.Value)
		m.mu.Unlock()
	}
	return res, err
}

// requireMade fails the test unless the held write has been made: it is on its
// way to the manager and waits for letGo.
func (m *modelWriteTrace) requireMade(t *testing.T) {
	t.Helper()
	select {
	case <-m.entered:
	default:
		t.Fatalf("the write of %q was never made", m.held)
	}
}

// letGo lets the held write reach the manager.
func (m *modelWriteTrace) letGo() { m.letOnce.Do(func() { close(m.release) }) }

// writes is the models written into a session, in the order the writes landed.
func (m *modelWriteTrace) writes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.landed)
}

// failingModelWrites refuses the write of one model into a session while fail
// is set: a backend that will not put a session on the remembered model.
type failingModelWrites struct {
	backend
	model string
	fail  *atomic.Bool
}

func (f failingModelWrites) HandleSessionSetConfigOption(ctx context.Context, p acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if p.ConfigID == "model" && p.Value == f.model && f.fail.Load() {
		return nil, errors.New("provider refused " + p.Value)
	}
	return f.backend.HandleSessionSetConfigOption(ctx, p)
}

// Models of the startup tests: the alphabetical first, a remembered one and the
// configured default are three different ones.
const (
	startupAlphabetical = "stub/aaa-model"
	startupRemembered   = "stub/model-two"
	startupDefault      = "stub/zed-model"
)

// newModelStartupApp builds a console over a real manager in a temp home with
// the three models above, agent.model on the last. remembered is what the
// console's state file holds ("" for a first use); wrap, when set, puts a
// backend in front of the manager. The app is not started.
func newModelStartupApp(t *testing.T, remembered string, wrap func(backend) backend) *App {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{
			Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test",
		}},
		Models: []config.ModelEntry{
			{Model: startupDefault, MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: startupAlphabetical, MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: startupRemembered, MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: startupDefault},
	}
	if remembered != "" {
		saveConsoleState(home, consoleState{LastModel: remembered})
	}
	store := &session.FileStore{Root: filepath.Join(home, "sessions")}
	lateSender := &lateBoundSender{}
	mgr := session.NewManager(cfg, lateSender, nil, slog.New(slog.DiscardHandler), home, store)
	var be backend = mgr
	if wrap != nil {
		be = wrap(mgr)
	}
	app := newApp(cfg, be, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 35}, "dark", true)
	lateSender.inner = app.Sender()
	// Registered after TempDir, so it runs first: no write may be under way
	// when the home is removed.
	t.Cleanup(func() { app.JoinWorkers(5 * time.Second) })
	return app
}

func sessionModel(t *testing.T, a *App) string {
	t.Helper()
	st := a.mgr.SessionByID(a.sessionID)
	if st == nil {
		t.Fatalf("no live session %q", a.sessionID)
	}
	return st.EffectiveModelID(a.config())
}

// startupVariant is one way the console opens its first session.
type startupVariant struct {
	name   string
	resume bool
	// cancelPicker leaves a session to resume in the folder and cancels the
	// picker that offers it: the picker's other way to a new session.
	cancelPicker bool
}

var startupVariants = []startupVariant{
	{name: "new session"},
	{name: "resume with no session to resume", resume: true},
	{name: "resume picker cancelled", resume: true, cancelPicker: true},
}

// run starts the console the way coddy does before raw mode - Start, then
// after (the command line's options) when there is one - in a goroutine of its
// own, and returns the channel that carries the first error or nil.
func (v startupVariant) run(t *testing.T, app *App, after func(ctx context.Context) error) <-chan error {
	t.Helper()
	if v.cancelPicker {
		home := app.config().Paths.Home
		writeSnapshotFixture(t, filepath.Join(home, "sessions"), "sess_prior", home, time.Now().Add(-time.Hour))
	}
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		if err := app.Start(ctx, "", v.resume); err != nil {
			done <- err
			return
		}
		if after == nil {
			done <- nil
			return
		}
		done <- after(ctx)
	}()
	if v.cancelPicker {
		// The picker reads the keys in order, so the key may be sent before it
		// has opened: it is read once the picker is up.
		app.OnTerminalInput([]byte("\x1b"))
	}
	return done
}

// The console's initial model is on the session, and in what the console
// shows, when Start returns - never a write still on its way. The write is held
// at the manager until the test lets it go, so a Start that does not wait for
// it is caught whatever the scheduler does.
func TestStartAppliesTheInitialModelBeforeItReturns(t *testing.T) {
	picks := []struct {
		name       string
		remembered string
		want       string
	}{
		{name: "remembered pick", remembered: startupRemembered, want: startupRemembered},
		{name: "first use is the alphabetical first", remembered: "", want: startupAlphabetical},
	}
	for _, pick := range picks {
		for _, v := range startupVariants {
			t.Run(pick.name+"/"+v.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					trace := newModelWriteTrace(t, pick.want)
					app := newModelStartupApp(t, pick.remembered, trace.around)
					started := v.run(t, app, nil)
					// Everything Start does without the write is done: it has
					// returned, or it waits for the write.
					synctest.Wait()
					trace.requireMade(t)
					select {
					case err := <-started:
						t.Fatalf("Start returned (error %v) while the write of the initial model %q was still on its way", err, pick.want)
					default:
					}
					trace.letGo()
					if err := <-started; err != nil {
						t.Fatal(err)
					}
					if got := sessionModel(t, app); got != pick.want {
						t.Fatalf("session model right after Start = %q, want %q", got, pick.want)
					}
					if app.modelID != pick.want {
						t.Fatalf("model the console shows right after Start = %q, want %q", app.modelID, pick.want)
					}
				})
			})
		}
	}
}

// --model is the operator's choice for the run: it is written after the
// remembered model whatever the scheduler does. The write of the remembered
// model is held at the manager until everything the startup does without it is
// done - the worst order a late goroutine could give - and the writes must
// still land remembered first, --model last.
func TestStartupModelFlagBeatsTheRememberedModel(t *testing.T) {
	for _, v := range startupVariants {
		t.Run(v.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				trace := newModelWriteTrace(t, startupRemembered)
				app := newModelStartupApp(t, startupRemembered, trace.around)
				done := v.run(t, app, func(ctx context.Context) error {
					return app.ApplyStartupOptions(ctx, startupAlphabetical, "", "")
				})
				// The startup has applied --model, or waits for the write of
				// the remembered model.
				synctest.Wait()
				trace.requireMade(t)
				trace.letGo()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				// Every write the startup started has finished.
				app.JoinWorkers(5 * time.Second)
				if got, want := trace.writes(), []string{startupRemembered, startupAlphabetical}; !slices.Equal(got, want) {
					t.Fatalf("model writes landed in the order %v, want %v", got, want)
				}
				if got := sessionModel(t, app); got != startupAlphabetical {
					t.Fatalf("session model = %q, want the --model value %q", got, startupAlphabetical)
				}
				if app.modelID != startupAlphabetical {
					t.Fatalf("model the console shows = %q, want %q", app.modelID, startupAlphabetical)
				}
			})
		})
	}
}

// refusalText is what the console tells the operator when the backend refuses
// the write of the remembered model.
const refusalText = "model: provider refused " + startupRemembered

// drainUpdates takes what is queued for the UI loop.
func drainUpdates(a *App) []updateMsg {
	var out []updateMsg
	for {
		select {
		case m := <-a.updatesCh:
			out = append(out, m)
		default:
			return out
		}
	}
}

// statusLines picks the status lines out of queued updates: their index and
// the messages that carry them.
func statusLines(msgs []updateMsg) (at []int, got []updateMsg) {
	for i, m := range msgs {
		if _, ok := m.update.(statusErr); ok {
			at = append(at, i)
			got = append(got, m)
		}
	}
	return at, got
}

// applyAndCount runs the queued updates through the UI loop in order and says
// how often the refusal ended up in the transcript: an update the loop drops is
// never shown.
func applyAndCount(a *App, msgs []updateMsg) int {
	for _, m := range msgs {
		a.applyLoopMessage(m)
	}
	return strings.Count(transcriptText(a), refusalText)
}

// A refused write of the initial model leaves the session on the model it was
// created with and is reported once, on the session the console is on, however
// the first session was opened.
func TestStartReportsARefusedInitialModelOnce(t *testing.T) {
	for _, v := range startupVariants {
		t.Run(v.name, func(t *testing.T) {
			fail := &atomic.Bool{}
			fail.Store(true)
			app := newModelStartupApp(t, startupRemembered, func(b backend) backend {
				return failingModelWrites{backend: b, model: startupRemembered, fail: fail}
			})
			done := v.run(t, app, nil)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Start did not return")
			}
			if got := sessionModel(t, app); got != startupDefault {
				t.Fatalf("session model = %q, want the model it was created with, %q", got, startupDefault)
			}
			msgs := drainUpdates(app)
			_, got := statusLines(msgs)
			if len(got) != 1 {
				t.Fatalf("%d status lines queued, want exactly the refusal: %+v", len(got), got)
			}
			if got[0].sessionID != app.sessionID || app.sessionID == "" {
				t.Fatalf("the refusal names session %q, the console is on %q", got[0].sessionID, app.sessionID)
			}
			if e := got[0].update.(statusErr); e.msg != refusalText {
				t.Fatalf("refusal = %q, want %q", e.msg, refusalText)
			}
			if n := applyAndCount(app, msgs); n != 1 {
				t.Fatalf("the transcript shows the refusal %d times, want once:\n%s", n, transcriptText(app))
			}
		})
	}
}

// On /new the console shows the remembered model from the switch on. The
// write's own config update names the new session and is queued before
// sessionSwitched, so the loop drops it as another session's: the switch has to
// carry the options the write returned. The first session runs on --model,
// which does not rewrite the memory, so the console shows a third model until
// the switch.
func TestNewSessionShowsTheRememberedModelFromTheSwitch(t *testing.T) {
	app := newModelStartupApp(t, startupRemembered, nil)
	ctx := context.Background()
	if err := app.Start(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	if err := app.ApplyStartupOptions(ctx, startupAlphabetical, "", ""); err != nil {
		t.Fatal(err)
	}
	old := app.sessionID
	for _, m := range drainUpdates(app) {
		app.applyLoopMessage(m)
	}
	if app.modelID != startupAlphabetical {
		t.Fatalf("model the console shows before /new = %q, want the --model value %q", app.modelID, startupAlphabetical)
	}

	app.newSession()
	app.JoinWorkers(10 * time.Second)
	msgs := drainUpdates(app)
	if _, got := statusLines(msgs); len(got) != 0 {
		t.Fatalf("%d status lines queued, want none: %+v", len(got), got)
	}
	for _, m := range msgs {
		app.applyLoopMessage(m)
	}
	if app.sessionID == old || app.sessionID == "" {
		t.Fatalf("/new left the console on %q (was %q)", app.sessionID, old)
	}
	if got := sessionModel(t, app); got != startupRemembered {
		t.Fatalf("session model after /new = %q, want %q", got, startupRemembered)
	}
	if app.modelID != startupRemembered {
		t.Fatalf("model the console shows after /new = %q, want %q", app.modelID, startupRemembered)
	}
}

// On /new the refusal follows sessionSwitched: until the loop has taken that
// message the console is still on the old session and drops a status line sent
// for the new one. It is reported once, and for the new session.
func TestNewSessionReportsARefusedInitialModelAfterTheSwitch(t *testing.T) {
	fail := &atomic.Bool{}
	app := newModelStartupApp(t, startupRemembered, func(b backend) backend {
		return failingModelWrites{backend: b, model: startupRemembered, fail: fail}
	})
	if err := app.Start(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	old := app.sessionID
	drainUpdates(app)

	fail.Store(true)
	app.newSession()
	app.JoinWorkers(10 * time.Second)
	msgs := drainUpdates(app)

	switched := -1
	for i, m := range msgs {
		if _, ok := m.update.(sessionSwitched); ok {
			if switched >= 0 {
				t.Fatalf("session switched twice: updates %d and %d", switched, i)
			}
			switched = i
		}
	}
	if switched < 0 {
		t.Fatalf("no sessionSwitched among the queued updates: %+v", msgs)
	}
	next := msgs[switched].update.(sessionSwitched).res.SessionID
	if next == "" || next == old {
		t.Fatalf("/new switched to %q from %q", next, old)
	}
	at, got := statusLines(msgs)
	if len(got) != 1 {
		t.Fatalf("%d status lines queued, want exactly the refusal: %+v", len(got), got)
	}
	if at[0] < switched {
		t.Fatalf("the refusal is update %d, before sessionSwitched (%d): the console would drop it", at[0], switched)
	}
	if got[0].sessionID != next {
		t.Fatalf("the refusal names session %q, /new switched to %q", got[0].sessionID, next)
	}
	if e := got[0].update.(statusErr); e.msg != refusalText {
		t.Fatalf("refusal = %q, want %q", e.msg, refusalText)
	}
	if n := applyAndCount(app, msgs); n != 1 {
		t.Fatalf("the transcript shows the refusal %d times, want once:\n%s", n, transcriptText(app))
	}
}
