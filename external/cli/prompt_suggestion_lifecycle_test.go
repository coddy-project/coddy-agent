//go:build cli

package cli

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newPromptSuggestionConsole(t *testing.T, providerURL string) (*App, *session.State) {
	t.Helper()
	home := t.TempDir()
	blocking, zero := false, 0
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{Name: "test", Type: "openai", APIBase: providerURL, APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "test/model", Stream: &blocking}},
		Agent:     config.Agent{Model: "test/model", LLMRetryMax: &zero},
	}
	sender := &lateBoundSender{}
	mgr := session.NewManager(cfg, sender, nil, discardLogger(), home, nil)
	t.Cleanup(mgr.CloseMCP)
	app := newApp(cfg, mgr, discardLogger(), &bddTerminal{cols: 80, rows: 24}, "dark", false)
	sender.inner = app.Sender()
	t.Cleanup(func() {
		app.stopSpinner()
		app.Close()
		app.workers.Wait()
	})
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	app.sessionID = res.SessionID
	state := mgr.SessionByID(res.SessionID)
	state.AddMessage(llm.Message{Role: llm.RoleUser, Content: "fix the bug and run the tests"})
	state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Fixed the bug."})
	return app, state
}

// Wait for posted callbacks without running another turnDone or touching UI
// state from a worker. This also makes a negative assertion wait for the result.
func settlePromptSuggestionUI(t *testing.T, app *App) {
	t.Helper()
	done := make(chan struct{})
	go func() { app.workers.Wait(); close(done) }()
	timer := time.NewTimer(within(5 * time.Second))
	defer timer.Stop()
	apply := func(msg updateMsg) {
		if _, ok := msg.update.(uiCall); ok {
			app.applyLoopMessage(msg)
		}
	}
	for {
		select {
		case msg := <-app.updatesCh:
			apply(msg)
		case <-done:
			for {
				select {
				case msg := <-app.updatesCh:
					apply(msg)
				default:
					return
				}
			}
		case <-timer.C:
			t.Fatal("console workers did not finish")
		}
	}
}

type promptSuggestionOutcome struct {
	sessionID string
	accepted  bool
}

type controlledSuggestionBackend struct {
	backend
	generate func(context.Context, string) (string, error)
	outcomes chan promptSuggestionOutcome
}

func (b *controlledSuggestionBackend) GeneratePromptSuggestion(ctx context.Context, id string) (string, error) {
	return b.generate(ctx, id)
}

func (b *controlledSuggestionBackend) NotePromptSuggestionOutcome(id string, accepted bool) {
	b.outcomes <- promptSuggestionOutcome{id, accepted}
}

func (b *controlledSuggestionBackend) HandleSessionPromptWithSender(context.Context, acp.SessionPromptParams, acp.UpdateSender, *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	return &acp.SessionPromptResult{StopReason: "end_turn"}, nil
}

func TestCLIPromptSuggestionRejectsLateResults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*App)
		cancels   bool
		wantDraft string
	}{
		{"typed draft", func(a *App) { a.editor.HandleInput([]byte("my own prompt")) }, false, "my own prompt"},
		{"session switch", func(a *App) { a.adoptSession("sess_other", nil, nil) }, true, ""},
		{"new turn", func(a *App) {
			a.startTurnWorker(acp.SessionPromptParams{SessionID: a.sessionID}, nil, nil)
		}, true, ""},
		{"configuration reload", func(a *App) { a.applyLoopMessage(updateMsg{update: configReloaded{}}) }, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newPromptSuggestionConsole(t, "http://unused.invalid")
			started, release := make(chan context.Context, 1), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			app.mgr = &controlledSuggestionBackend{backend: app.mgr, generate: func(ctx context.Context, _ string) (string, error) {
				started <- ctx
				// Deliberately return an answer even after cancellation: transports
				// can deliver a result just as the operator leaves this turn.
				<-release
				return "run the tests", nil
			}}
			app.startSuggestion(app.sessionID)
			var callCtx context.Context
			select {
			case callCtx = <-started:
			case <-time.After(within(5 * time.Second)):
				t.Fatal("no suggestion request")
			}
			tc.change(app)
			if tc.cancels && callCtx.Err() == nil {
				t.Error("obsolete suggestion request was not cancelled")
			}
			once.Do(func() { close(release) })
			settlePromptSuggestionUI(t, app)
			if app.editor.Placeholder() != "" || app.suggestion != nil || app.editor.Text() != tc.wantDraft {
				t.Fatalf("late result changed the editor: hint=%q draft=%q", app.editor.Placeholder(), app.editor.Text())
			}
		})
	}
}

func TestCLIPromptSuggestionConfigReloadRetiresVisibleHint(t *testing.T) {
	app, _ := newPromptSuggestionConsole(t, "http://unused.invalid")
	app.applySuggestion(app.suggestionGen, app.sessionID, "run the tests", nil)
	next := *app.config()
	off := false
	next.Agent.PromptSuggestions = &off
	app.cfgAt.Store(&next)
	app.applyLoopMessage(updateMsg{update: configReloaded{}})
	if app.editor.Placeholder() != "" || app.suggestion != nil {
		t.Fatalf("disabled suggestions left a visible hint: %q", app.editor.Placeholder())
	}
}

func TestCLIPromptSuggestionFailureLeavesEditorEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		err  error
	}{
		{"provider failure", "", errors.New("provider unavailable")},
		{"cancelled request", "", context.Canceled},
		{"model silence", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newPromptSuggestionConsole(t, "http://unused.invalid")
			app.mgr = &controlledSuggestionBackend{backend: app.mgr, generate: func(context.Context, string) (string, error) { return tc.text, tc.err }}
			app.startSuggestion(app.sessionID)
			settlePromptSuggestionUI(t, app)
			if app.editor.Text() != "" || app.editor.Placeholder() != "" || app.suggestion != nil {
				t.Fatal("failed request left text in the editor")
			}
			if transcriptText(app) != "" {
				t.Fatalf("suggestion failure leaked into the conversation: %s", transcriptText(app))
			}
		})
	}
}

func TestCLIPromptSuggestionSkippedAfterUnusableTurn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft string
		stop  string
		err   error
	}{
		{"cancelled turn", "", "cancelled", nil},
		{"failed turn", "", "", errors.New("turn failed")},
		{"existing draft", "my next prompt", "end_turn", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newPromptSuggestionConsole(t, "http://unused.invalid")
			var calls atomic.Int32
			app.mgr = &controlledSuggestionBackend{backend: app.mgr, generate: func(context.Context, string) (string, error) {
				calls.Add(1)
				return "run the tests", nil
			}}
			app.editor.SetText(tc.draft)
			app.turnSessionID, app.turnActive = app.sessionID, true
			app.applyLoopMessage(updateMsg{update: turnDone{sessionID: app.sessionID, stop: tc.stop, err: tc.err}})
			settlePromptSuggestionUI(t, app)
			if calls.Load() != 0 || app.editor.Placeholder() != "" || app.editor.Text() != tc.draft {
				t.Fatalf("unexpected prediction: calls=%d hint=%q draft=%q", calls.Load(), app.editor.Placeholder(), app.editor.Text())
			}
		})
	}
}

func TestCLIPromptSuggestionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      string
		text     string
		accepted bool
	}{
		{"Tab", "\t", "run the tests", true},
		{"Right", "\x1b[C", "run the tests", true},
		{"retyped", "", " RUN THE TESTS ", true},
		{"ignored", "", "explain the changes", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := newPromptSuggestionConsole(t, "http://unused.invalid")
			outcomes := make(chan promptSuggestionOutcome, 2)
			app.mgr = &controlledSuggestionBackend{backend: app.mgr, outcomes: outcomes}
			app.applySuggestion(app.suggestionGen, app.sessionID, "run the tests", nil)
			if tc.key != "" {
				app.editor.HandleInput([]byte(tc.key))
				if app.editor.Text() != "run the tests" {
					t.Fatalf("key did not accept the hint: %q", app.editor.Text())
				}
			}
			app.settleSuggestion(tc.text)
			app.settleSuggestion(tc.text)
			settlePromptSuggestionUI(t, app)
			if len(outcomes) != 1 {
				t.Fatalf("reported %d outcomes for one hint", len(outcomes))
			}
			if got := <-outcomes; got.sessionID != app.sessionID || got.accepted != tc.accepted {
				t.Fatalf("outcome = %+v, want accepted=%v", got, tc.accepted)
			}
			if app.editor.Placeholder() != "" || app.suggestion != nil {
				t.Fatal("submitted hint was not retired")
			}
		})
	}
}
