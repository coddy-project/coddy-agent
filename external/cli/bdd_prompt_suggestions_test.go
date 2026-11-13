//go:build cli

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func TestCLIPromptSuggestionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "cli-prompt-suggestions",
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"../../features/cli_prompt_suggestions.feature"}, TestingT: t, Strict: true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var app *App
			var state *session.State
			var before []llm.Message
			var calls *atomic.Int32
			sc.Step(`^a local console whose model predicts "([^"]*)"$`, func(text string) {
				counter := &atomic.Int32{}
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					counter.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
					})
				}))
				t.Cleanup(provider.Close)
				app, state = newPromptSuggestionConsole(t, provider.URL)
				before, calls = state.GetMessages(), counter
			})
			sc.Step(`^console prompt suggestions are disabled$`, func() {
				off := false
				app.config().Agent.PromptSuggestions = &off
			})
			sc.Step(`^the first console turn finishes$`, func() {
				app.turnSessionID, app.turnActive = app.sessionID, true
				app.applyLoopMessage(updateMsg{update: turnDone{sessionID: app.sessionID, stop: "end_turn"}})
				settlePromptSuggestionUI(t, app)
			})
			sc.Step(`^the empty editor shows "([^"]*)" in its dim color$`, func(text string) error {
				if app.editor.Text() != "" || app.editor.Placeholder() != text {
					return fmt.Errorf("draft=%q hint=%q, want an empty draft and hint %q", app.editor.Text(), app.editor.Placeholder(), text)
				}
				if rendered := strings.Join(app.editor.Render(80), "\n"); !strings.Contains(rendered, app.theme.Fg(roleDim, text)) {
					return fmt.Errorf("hint has no dim color in %q", rendered)
				}
				return nil
			})
			sc.Step(`^the prediction leaves the conversation unchanged$`, func() error {
				if !reflect.DeepEqual(state.GetMessages(), before) {
					return fmt.Errorf("prediction changed the transcript")
				}
				return nil
			})
			sc.Step(`^the operator accepts the console hint with (Tab|Right)$`, func(key string) {
				input := "\t"
				if key == "Right" {
					input = "\x1b[C"
				}
				app.dispatchInput([]byte(input))
			})
			sc.Step(`^the console draft is "([^"]*)"$`, func(text string) error {
				if app.editor.Text() != text || app.suggestion == nil || !app.suggestion.accepted {
					return fmt.Errorf("hint was not accepted into the draft: %q", app.editor.Text())
				}
				return nil
			})
			sc.Step(`^only one console prediction was requested$`, func() error {
				if calls.Load() != 1 {
					return fmt.Errorf("prediction requests=%d, want 1", calls.Load())
				}
				return nil
			})
			sc.Step(`^the console editor offers no hint$`, func() error {
				if app.editor.Placeholder() != "" || app.suggestion != nil || app.editor.Text() != "" {
					return fmt.Errorf("disabled prediction affected the editor")
				}
				return nil
			})
			sc.Step(`^no console prediction was requested$`, func() error {
				if calls.Load() != 0 {
					return fmt.Errorf("disabled predictions made %d model calls", calls.Load())
				}
				return nil
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("console prompt suggestion scenarios failed")
	}
}
