//go:build cli

package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
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

// lateModelWriteDelay is how long lateModelWrites holds a write back: far
// longer than the rest of a startup takes, so a writer that is not waited for
// always loses the race it is meant to lose.
const lateModelWriteDelay = 200 * time.Millisecond

// lateModelWrites holds the write of one model into a session back by delay,
// the way a goroutine the scheduler runs late would reach the manager. It
// orders the two writers of a console session's model deterministically: a
// startup that does not wait for the write sees it land after everything else.
type lateModelWrites struct {
	backend
	model string
	delay time.Duration
}

func (l lateModelWrites) HandleSessionSetConfigOption(ctx context.Context, p acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if p.ConfigID == "model" && p.Value == l.model {
		time.Sleep(l.delay)
	}
	return l.backend.HandleSessionSetConfigOption(ctx, p)
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
// console's state file holds ("" for a first use); the write of late into a
// session is held back. The app is not started.
func newModelStartupApp(t *testing.T, remembered, late string) *App {
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
	app := newApp(cfg, lateModelWrites{backend: mgr, model: late, delay: lateModelWriteDelay}, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 35}, "dark", true)
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

// The console's initial model is on the session, and in what the console
// shows, when Start returns - never a write still on its way - for a new
// session and for the one the resume picker opens when there is none to resume.
func TestStartAppliesTheInitialModelBeforeItReturns(t *testing.T) {
	cases := []struct {
		name       string
		remembered string
		want       string
	}{
		{name: "remembered pick", remembered: startupRemembered, want: startupRemembered},
		{name: "first use is the alphabetical first", remembered: "", want: startupAlphabetical},
	}
	for _, tc := range cases {
		for _, resume := range []bool{false, true} {
			name := tc.name
			if resume {
				name += " with resume"
			}
			t.Run(name, func(t *testing.T) {
				app := newModelStartupApp(t, tc.remembered, tc.want)
				if err := app.Start(context.Background(), "", resume); err != nil {
					t.Fatal(err)
				}
				if got := sessionModel(t, app); got != tc.want {
					t.Fatalf("session model right after Start = %q, want %q", got, tc.want)
				}
				if app.modelID != tc.want {
					t.Fatalf("model the console shows right after Start = %q, want %q", app.modelID, tc.want)
				}
			})
		}
	}
}

// --model is the operator's choice for the run: it wins over the remembered
// model whatever order the scheduler gives the two writes.
func TestStartupModelFlagBeatsTheRememberedModel(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "new session"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			app := newModelStartupApp(t, startupRemembered, startupRemembered)
			ctx := context.Background()
			if err := app.Start(ctx, "", resume); err != nil {
				t.Fatal(err)
			}
			if err := app.ApplyStartupOptions(ctx, startupAlphabetical, "", ""); err != nil {
				t.Fatal(err)
			}
			// Every write the startup started has finished.
			app.JoinWorkers(5 * time.Second)
			if got := sessionModel(t, app); got != startupAlphabetical {
				t.Fatalf("session model = %q, want the --model value %q", got, startupAlphabetical)
			}
			if app.modelID != startupAlphabetical {
				t.Fatalf("model the console shows = %q, want %q", app.modelID, startupAlphabetical)
			}
		})
	}
}

// The resume picker's other way to a new session - the operator cancels it
// with sessions on offer - starts on the initial model too, and --model still
// beats it.
func TestResumePickerCancelledStartsOnTheInitialModel(t *testing.T) {
	app := newModelStartupApp(t, startupRemembered, startupRemembered)
	home := app.config().Paths.Home
	writeSnapshotFixture(t, filepath.Join(home, "sessions"), "sess_prior", home, time.Now().Add(-time.Hour))

	ctx := context.Background()
	started := make(chan error, 1)
	go func() { started <- app.Start(ctx, "", true) }()
	// The picker reads the keys in order, so the key may be sent before it
	// has opened: it is read once the picker is up.
	app.OnTerminalInput([]byte("\x1b"))
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the resume picker was cancelled")
	}
	if got := sessionModel(t, app); got != startupRemembered {
		t.Fatalf("session model right after Start = %q, want %q", got, startupRemembered)
	}
	if err := app.ApplyStartupOptions(ctx, startupAlphabetical, "", ""); err != nil {
		t.Fatal(err)
	}
	app.JoinWorkers(5 * time.Second)
	if got := sessionModel(t, app); got != startupAlphabetical {
		t.Fatalf("session model = %q, want the --model value %q", got, startupAlphabetical)
	}
}
