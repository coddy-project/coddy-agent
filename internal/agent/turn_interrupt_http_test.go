package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type queuedHTTPDeltaSender struct {
	acp.UpdateSender
	firstDelta chan struct{}
	once       sync.Once
}

func (s *queuedHTTPDeltaSender) SendSessionUpdate(id string, update interface{}) error {
	if chunk, ok := update.(acp.MessageChunkUpdate); ok && chunk.SessionUpdate == acp.UpdateTypeAgentMessageChunk && chunk.Content.Text == "partial over HTTP" {
		s.once.Do(func() { close(s.firstDelta) })
	}
	return s.UpdateSender.SendSessionUpdate(id, update)
}

// The real HTTP transport returns context.Cause, not ctx.Err. Its partial
// response and the manager's continuation both require a cancellation chain.
func TestQueuedInterruptRealOpenAITransport(t *testing.T) {
	for _, queued := range []bool{true, false} {
		t.Run(fmt.Sprintf("queued=%v", queued), func(t *testing.T) {
			type wireMessage struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			var mu sync.Mutex
			var requests [][]wireMessage
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model"}]}`)
					return
				}
				var request struct {
					Messages []wireMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("read provider request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				call := len(requests)
				requests = append(requests, request.Messages)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if call == 0 {
					_, _ = io.WriteString(w, "data: {\"id\":\"first\",\"object\":\"chat.completion.chunk\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial over HTTP\"},\"finish_reason\":null}]}\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, "data: {\"id\":\"second\",\"object\":\"chat.completion.chunk\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answered queued rows\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()

			home, cwd := t.TempDir(), t.TempDir()
			stream, zero := true, 0
			cfg := &config.Config{
				Paths:     config.Paths{Home: home, CWD: cwd, ConfigPath: filepath.Join(home, "config.yaml")},
				Providers: []config.ProviderConfig{{Name: "fixture", Type: "openai", APIBase: upstream.URL, APIKey: "fixture-only", Proxy: "none"}},
				Models:    []config.ModelEntry{{Model: "fixture/model", MaxTokens: 100, MaxContextTokens: 128000, Stream: &stream}},
				Agent:     config.Agent{Model: "fixture/model", MaxTurns: 6, LLMRetryMax: &zero},
				Tools:     config.Tools{PermissionMode: config.PermModeBypass},
				Sessions:  config.Sessions{Dir: filepath.Join(home, "sessions")},
			}
			cfg.Prompts.ApplyDefaults()
			cfg.Hooks.ApplyDefaults(cfg.Paths)
			cfg.Subagents.ApplyDefaults(cfg.Paths)
			sender := &queuedHTTPDeltaSender{UpdateSender: &recordingClient{answer: "allow"}, firstDelta: make(chan struct{})}
			var interruptCause error
			runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
				// No provider factory override: exercise the configured OpenAI
				// provider, resilient wrapper, HTTP client and SSE reader.
				stop, err := NewAgent(cfg, st, snd, slog.Default()).Run(ctx, prompt)
				if session.IsQueuedTurnInterrupt(ctx, st.GetID()) {
					interruptCause = context.Cause(ctx)
				}
				return stop, err
			}
			store := &session.FileStore{Root: cfg.Sessions.Dir}
			m := session.NewManager(cfg, sender, runner, slog.Default(), cwd, store)
			created, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
			if err != nil {
				t.Fatal(err)
			}
			sid := created.SessionID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				result *acp.SessionPromptResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "original"}}})
				done <- outcome{result, err}
			}()
			select {
			case <-sender.firstDelta:
			case <-time.After(5 * time.Second):
				t.Fatal("real provider never delivered its first text delta")
			}
			texts := []string{"first queued row", "duplicate queued row", "duplicate queued row"}
			if queued {
				for i, text := range texts {
					mode := session.QueueModeAfterTurn
					if i == 1 {
						mode = session.QueueModeSteer
					}
					if _, _, err := m.EnqueueTurnMessageWithMode(sid, text, mode, nil); err != nil {
						t.Fatal(err)
					}
				}
				if !m.TryInterruptQueuedTurn(sid) {
					t.Fatal("real provider queue interrupt refused")
				}
			} else {
				if m.TryInterruptQueuedTurn(sid) {
					t.Fatal("empty queue accepted an interrupt")
				}
				m.HandleSessionCancel(acp.SessionCancelParams{SessionID: sid})
			}
			var got outcome
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("real provider cancellation did not settle")
			}
			if queued && !errors.Is(interruptCause, context.Canceled) {
				t.Errorf("interrupt cause has no cancellation chain: %v", interruptCause)
			}
			if got.err != nil {
				t.Fatalf("real HTTP continuation failed: %v; history=%+v; queue=%+v", got.err, m.SessionByID(sid).GetMessages(), m.SessionByID(sid).QueuedMessages())
			}
			wantStop, wantCalls := acp.StopReasonEndTurn, 2
			if !queued {
				wantStop, wantCalls = acp.StopReasonCancelled, 1
			}
			if got.result == nil || got.result.StopReason != wantStop {
				t.Fatalf("result=%+v, want %s", got.result, wantStop)
			}
			messages := m.SessionByID(sid).GetMessages()
			if len(messages) < 2 || messages[0].Content != "original" || messages[1].Role != llm.RoleAssistant || messages[1].Content != "partial over HTTP" {
				t.Fatalf("real HTTP partial was not persisted before queued rows: %+v", messages)
			}
			if queued {
				if len(messages) != len(texts)+3 {
					t.Fatalf("history has %d rows, want %d: %+v", len(messages), len(texts)+3, messages)
				}
				for i, text := range texts {
					if msg := messages[i+2]; msg.Role != llm.RoleUser || msg.Content != text || !msg.FollowUp {
						t.Errorf("queued row %d=%+v", i, msg)
					}
				}
				if last := messages[len(messages)-1]; last.Role != llm.RoleAssistant || last.Content != "answered queued rows" {
					t.Fatalf("fresh HTTP call did not answer: %+v", last)
				}
				if rows := m.SessionByID(sid).QueuedMessages(); len(rows) != 0 {
					t.Fatalf("answered rows remained queued: %+v", rows)
				}
			}
			persisted, err := os.ReadFile(filepath.Join(store.SessionPath(sid), "messages.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(persisted), "partial over HTTP") {
				t.Fatal("partial answer is absent from persisted history")
			}
			mu.Lock()
			seen := append([][]wireMessage(nil), requests...)
			mu.Unlock()
			if len(seen) != wantCalls {
				t.Fatalf("real HTTP calls=%d, want %d", len(seen), wantCalls)
			}
			if queued {
				var users []string
				partial := false
				for _, msg := range seen[1] {
					var text string
					if err := json.Unmarshal(msg.Content, &text); err != nil {
						continue
					}
					if msg.Role == "assistant" && text == "partial over HTTP" {
						partial = true
					}
					if msg.Role == "user" && !strings.HasPrefix(text, "<turn_context>") {
						users = append(users, text)
					}
				}
				if !partial || len(users) != 4 || users[0] != "original" || users[1] != texts[0] || users[2] != texts[1] || users[3] != texts[2] {
					t.Fatalf("fresh HTTP request lost the partial answer or FIFO rows: partial=%v users=%q", partial, users)
				}
			}
		})
	}
}
