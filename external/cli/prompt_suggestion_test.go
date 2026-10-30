//go:build cli

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
			defer provider.Close()
			home := t.TempDir()
			blocking, zero := false, 0
			cfg := &config.Config{
				Paths:     config.Paths{Home: home, CWD: home},
				Providers: []config.ProviderConfig{{Name: "test", Type: "openai", APIBase: provider.URL, APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "test/model", Stream: &blocking}},
				Agent:     config.Agent{Model: "test/model", LLMRetryMax: &zero},
			}
			sender := &lateBoundSender{}
			mgr := session.NewManager(cfg, sender, nil, discardLogger(), home, nil)
			defer mgr.CloseMCP()
			app := newApp(cfg, mgr, discardLogger(), &bddTerminal{cols: 80, rows: 24}, "dark", false)
			sender.inner = app.Sender()
			defer func() {
				app.workStop()
				close(app.closed)
				app.workers.Wait()
			}()
			res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
			if err != nil {
				t.Fatal(err)
			}
			app.sessionID, app.turnSessionID, app.turnActive = res.SessionID, res.SessionID, true
			state := mgr.SessionByID(res.SessionID)
			state.AddMessage(llm.Message{Role: llm.RoleUser, Content: "fix the bug and run the tests"})
			state.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Fixed the bug."})
			revision := state.MessagesRev()
			app.applyLoopMessage(updateMsg{update: turnDone{sessionID: res.SessionID, stop: "end_turn"}})
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for app.editor.Placeholder() == "" {
				select {
				case msg := <-app.updatesCh:
					if _, ok := msg.update.(uiCall); ok {
						app.applyLoopMessage(msg)
					}
				case <-deadline.C:
					t.Fatalf("no suggestion after the first completed turn; provider calls=%d", calls.Load())
				}
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
