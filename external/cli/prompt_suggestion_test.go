//go:build cli

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFirstConsoleTurnOffersDimPromptSuggestion(t *testing.T) {
	for _, suggestion := range []string{"run the tests", "продолжай"} {
		t.Run(suggestion, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				calls.Add(1)
				content, finish := suggestion, "stop"
				// Like beam-free, this model spends tokens on reasoning before
				// producing text. A 64-token call cannot reach its answer.
				if request.MaxTokens < 256 {
					content, finish = "", "length"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content, "reasoning_content": "predicting the next prompt"}, "finish_reason": finish}},
				})
			}))
			t.Cleanup(provider.Close)
			app, state := newPromptSuggestionConsole(t, provider.URL)
			revision := state.MessagesRev()
			app.turnSessionID, app.turnActive = state.ID, true
			app.applyLoopMessage(updateMsg{update: turnDone{sessionID: state.ID, stop: "end_turn"}})
			settlePromptSuggestionUI(t, app)
			if app.editor.Placeholder() != suggestion {
				t.Fatalf("no suggestion after the first completed turn; provider calls=%d", calls.Load())
			}
			if app.editor.Text() != "" || state.MessagesRev() != revision {
				t.Fatal("a suggestion must leave the draft and transcript untouched")
			}
			rendered := strings.Join(app.editor.Render(80), "\n")
			if !strings.Contains(rendered, app.theme.Fg(roleDim, suggestion)) {
				t.Fatalf("editor did not render the hint in its dim color: %q", rendered)
			}
			app.editor.HandleInput([]byte("\t"))
			if app.editor.Text() != suggestion || !app.suggestion.accepted {
				t.Fatalf("Tab did not accept the suggestion: %q", app.editor.Text())
			}
		})
	}
}
