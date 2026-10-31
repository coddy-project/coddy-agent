//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestRemoteSuggestionsUseServerConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body struct {
					Model    string            `json:"model"`
					Messages []llm.Message     `json:"messages"`
					Tools    []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid JSON", 400)
					return
				}
				if body.Model != "prediction-model" || len(body.Tools) != 1 || len(body.Messages) != 20 {
					t.Errorf("prediction did not fork the server model's main context")
				} else if body.Messages[0].Content != "MAIN_SYSTEM_PROMPT" || body.Messages[1].Content != "ORIGINAL_REQUEST" || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "SUGGESTION MODE") {
					t.Errorf("prediction did not inherit the complete main context")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"run the tests"},"finish_reason":"stop"}]}`)
			}))
			defer provider.Close()
			home := t.TempDir()
			stream := false
			cfg := &config.Config{
				Paths:      config.Paths{Home: home, CWD: home},
				HTTPServer: config.HTTPServerConfig{AuthToken: "suggestion-test-token"},
				Providers:  []config.ProviderConfig{{Name: "server", Type: "openai", APIBase: provider.URL, APIKey: "test"}},
				Models:     []config.ModelEntry{{Model: "server/prediction-model", Stream: &stream}},
				Agent:      config.Agent{Model: "server/prediction-model", PromptSuggestions: &tc.enabled},
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := session.NewManager(cfg, noopSender{}, nil, log, home, nil)
			defer mgr.CloseMCP()
			created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
			if err != nil {
				t.Fatal(err)
			}
			st := mgr.SessionByID(created.SessionID)
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "ORIGINAL_REQUEST"})
			for range 8 {
				st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Finished one step."})
				st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Continue."})
			}
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The fix is ready."})
			fork := append([]llm.Message{{Role: llm.RoleSystem, Content: "MAIN_SYSTEM_PROMPT"}}, session.MessagesForLLM(st.GetMessages())...)
			st.SetPromptSuggestionFork(fork, []llm.ToolDefinition{{Name: "read", Description: "Read a file", InputSchema: map[string]interface{}{"type": "object"}}})
			before := st.MessagesRev()
			srv := New(cfg, mgr, log, home)
			defer srv.Drain()
			api := httptest.NewServer(srv.Handler())
			defer api.Close()
			h, err := remote.NewHandler(remote.Options{BaseURL: api.URL, Token: "suggestion-test-token", Log: log})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			want := ""
			if tc.enabled {
				want = "run the tests"
			}
			for range 2 {
				text, err := h.GeneratePromptSuggestion(context.Background(), created.SessionID)
				if err != nil || text != want {
					t.Fatalf("remote suggestion=%q error=%v, want %q", text, err, want)
				}
			}
			wantCalls := int32(0)
			if tc.enabled {
				wantCalls = 1
			}
			if calls.Load() != wantCalls || st.MessagesRev() != before {
				t.Fatal("remote prediction duplicated the model call or changed the transcript")
			}
			if !tc.enabled {
				return
			}
			for _, accepted := range []bool{false, true} {
				h.NotePromptSuggestionOutcome(created.SessionID, accepted)
				data, err := os.ReadFile(filepath.Join(home, "prompt-suggestions.json"))
				if err != nil {
					t.Fatal(err)
				}
				var state struct {
					UnusedStreak int `json:"unused_streak"`
				}
				if err := json.Unmarshal(data, &state); err != nil {
					t.Fatal(err)
				}
				wantStreak := 1
				if accepted {
					wantStreak = 0
				}
				if state.UnusedStreak != wantStreak {
					t.Fatalf("server unused streak=%d, want %d", state.UnusedStreak, wantStreak)
				}
			}
		})
	}
}
