//go:build cli

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/cucumber/godog"
)

func newRemoteSuggestionConsole(t *testing.T, respond http.HandlerFunc) *App {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer suggestion-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	h, err := remote.NewHandler(remote.Options{BaseURL: srv.URL, Token: "suggestion-token", Log: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	// No local provider; the server owns the prediction switch and strategy.
	off := false
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Agent: config.Agent{PromptSuggestions: &off}}
	app := newApp(cfg, h, discardLogger(), &bddTerminal{cols: 80, rows: 24}, "dark", false)
	app.remoteURL, app.sessionID = h.BaseURL(), "sess_remote_suggestion"
	h.SetServer(app.Sender())
	t.Cleanup(func() { app.stopSpinner(); app.Close(); h.Close(); app.workers.Wait() })
	return app
}

func remoteHintReply(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.prompt_suggestion", "sessionId": "sess_remote_suggestion", "text": text})
}

func finishRemoteSuggestionTurn(t *testing.T, app *App) {
	t.Helper()
	app.turnActive, app.turnSessionID = true, app.sessionID
	app.applyLoopMessage(updateMsg{update: turnDone{sessionID: app.sessionID, stop: "end_turn"}})
	settlePromptSuggestionUI(t, app)
}

func TestRemoteConsolePromptSuggestionsFeature(t *testing.T) {
	var app *App
	var calls *atomic.Int32
	var outcomes chan bool
	suite := godog.TestSuite{Name: "remote-console-suggestions", Options: &godog.Options{Format: "progress", Paths: []string{"../../features/cli_remote_prompt_suggestions.feature"}, TestingT: t, Strict: true}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^a remote console whose server suggests "([^"]*)"$`, func(text string) {
			calls, outcomes = &atomic.Int32{}, make(chan bool, 2)
			counter, received := calls, outcomes
			app = newRemoteSuggestionConsole(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/prompt-suggestion"):
					counter.Add(1)
					remoteHintReply(w, text)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt-suggestion/outcome"):
					var body struct {
						Accepted bool `json:"accepted"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, "invalid JSON", 400)
						return
					}
					received <- body.Accepted
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})
		})
		sc.Step(`^the remote console turn finishes$`, func() { finishRemoteSuggestionTurn(t, app) })
		sc.Step(`^the remote editor shows the dim hint "([^"]*)"$`, func(text string) error {
			if app.editor.Text() != "" || app.editor.Placeholder() != text || !strings.Contains(strings.Join(app.editor.Render(80), "\n"), app.theme.Fg(roleDim, text)) {
				return fmt.Errorf("remote hint not displayed: %q", app.editor.Placeholder())
			}
			return nil
		})
		sc.Step(`^the remote hint is accepted with (Tab|Right) and submitted$`, func(key string) {
			input := "\t"
			if key == "Right" {
				input = "\x1b[C"
			}
			app.dispatchInput([]byte(input))
			app.settleSuggestion(app.editor.Text())
			settlePromptSuggestionUI(t, app)
		})
		sc.Step(`^the server receives an accepted suggestion outcome$`, func() error {
			select {
			case accepted := <-outcomes:
				if accepted {
					return nil
				}
			default:
			}
			return fmt.Errorf("server did not receive acceptance")
		})
		sc.Step(`^only one remote suggestion was requested$`, func() error {
			if calls.Load() != 1 {
				return fmt.Errorf("remote requests=%d", calls.Load())
			}
			return nil
		})
		sc.Step(`^the remote editor shows no hint$`, func() error {
			if app.editor.Placeholder() != "" || app.suggestion != nil {
				return fmt.Errorf("suppressed hint was shown")
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("remote console suggestion scenarios failed")
	}
}

func TestRemoteConsoleSuggestionWaitsForIdleEvent(t *testing.T) {
	var calls atomic.Int32
	app := newRemoteSuggestionConsole(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/prompt-suggestion") {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		remoteHintReply(w, "run the tests")
	})
	app.remoteTurnActive = true
	finishRemoteSuggestionTurn(t, app)
	if calls.Load() != 0 {
		t.Fatal("prediction requested while server still active")
	}
	app.applyLoopMessage(updateMsg{sessionID: app.sessionID, update: remote.ActivityUpdate{TurnActive: false, Revision: 2}})
	settlePromptSuggestionUI(t, app)
	if calls.Load() != 1 || app.editor.Placeholder() != "run the tests" {
		t.Fatal("idle event lost the deferred hint")
	}
	app.applyLoopMessage(updateMsg{sessionID: app.sessionID, update: remote.ActivityUpdate{TurnActive: true, Revision: 3}})
	if app.editor.Placeholder() != "" {
		t.Fatal("another remote turn kept the obsolete hint")
	}
}

func TestRemoteConsoleSuggestionCancelledByServerUpdate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update any
	}{
		{"configuration", remote.ConfigReloadedUpdate{}},
		{"new turn", remote.ActivityUpdate{TurnActive: true, Revision: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			app := newRemoteSuggestionConsole(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(cancelled)
			})
			app.startSuggestion(app.sessionID)
			select {
			case <-started:
			case <-time.After(within(5 * time.Second)):
				t.Fatal("prediction did not start")
			}
			app.applyLoopMessage(updateMsg{sessionID: app.sessionID, update: tc.update})
			select {
			case <-cancelled:
			case <-time.After(within(5 * time.Second)):
				t.Fatal("server request was not cancelled")
			}
			settlePromptSuggestionUI(t, app)
			if app.editor.Placeholder() != "" || app.suggestion != nil {
				t.Fatal("obsolete server hint returned")
			}
		})
	}
}

func TestRemoteConsoleReloadDropsDeferredSuggestion(t *testing.T) {
	var calls atomic.Int32
	app := newRemoteSuggestionConsole(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/prompt-suggestion") {
			calls.Add(1)
			remoteHintReply(w, "run the tests")
			return
		}
		http.NotFound(w, r)
	})
	app.remoteTurnActive = true
	finishRemoteSuggestionTurn(t, app)
	app.applyLoopMessage(updateMsg{update: remote.ConfigReloadedUpdate{}})
	app.applyLoopMessage(updateMsg{sessionID: app.sessionID, update: remote.ActivityUpdate{Revision: 2}})
	settlePromptSuggestionUI(t, app)
	if calls.Load() != 0 || app.editor.Placeholder() != "" {
		t.Fatal("reload retained the deferred prediction")
	}
}
