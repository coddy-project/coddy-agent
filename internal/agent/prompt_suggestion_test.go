package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPromptCachePromptSuggestionForkMatchesMainRequest(t *testing.T) {
	st := &session.State{ID: "sess_prompt_suggestion_fork", CWD: t.TempDir(), Mode: session.ModeAgent}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 2},
	}
	provider := &bddModelToolsProvider{}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "fix the bug"}}); err != nil {
		t.Fatal(err)
	}

	messages, tools, rev, ok := st.PromptSuggestionFork()
	if !ok {
		t.Fatal("completed turn did not store its suggestion fork")
	}
	if rev != st.MessagesRev() {
		t.Fatalf("fork revision = %d, current %d", rev, st.MessagesRev())
	}
	if len(provider.seen) != 1 || len(provider.offered) != 1 {
		t.Fatalf("provider calls = %d, want one", len(provider.seen))
	}
	if !reflect.DeepEqual(tools, provider.offered[0]) {
		t.Fatalf("fork tools differ from the main request")
	}
	mainRequest := provider.seen[0]
	if len(messages) != len(mainRequest)+1 {
		t.Fatalf("fork holds %d messages, want the main request plus its answer (%d)", len(messages), len(mainRequest)+1)
	}
	for i := range mainRequest {
		if mainRequest[i].Ephemeral {
			continue
		}
		if !reflect.DeepEqual(messages[i], mainRequest[i]) {
			t.Fatalf("fork message %d differs from the main request", i)
		}
	}
	if got := messages[len(mainRequest)-1]; got.Role != llm.RoleAssistant || got.Content != bddModelToolsAnswer {
		t.Fatalf("fork does not end its history with the main answer: %+v", got)
	}
	last := messages[len(messages)-1]
	if !last.Ephemeral || !reflect.DeepEqual(last, mainRequest[len(mainRequest)-1]) {
		t.Fatalf("fork lost the main request's trailing context: %+v", last)
	}
}

func TestPromptCachePromptSuggestionAfterRealTurn(t *testing.T) {
	var calls atomic.Int32
	var requests []struct {
		Messages []llm.Message     `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured struct {
			Messages []llm.Message     `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requests = append(requests, captured)
		answer := "run the tests"
		if calls.Add(1) == 1 {
			answer = "fixed the bug"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": answer}, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(provider.Close)
	home := t.TempDir()
	zero := 0
	blocking := false
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{Name: "p", Type: "openai", APIBase: provider.URL, APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "p/test", Stream: &blocking}},
		Agent:     config.Agent{Model: "p/test", MaxTurns: 2, LLMRetryMax: &zero},
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		return NewAgent(cfg, st, sender, nil).Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, resumePermissionSender{}, runner, slog.Default(), home, nil)
	t.Cleanup(mgr.CloseMCP)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: created.SessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "fix the bug and run the tests"}},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	before := st.MessagesRev()
	text, err := mgr.GeneratePromptSuggestion(context.Background(), created.SessionID)
	if err != nil || text != "run the tests" {
		t.Fatalf("suggestion = %q, %v", text, err)
	}
	if st.MessagesRev() != before || calls.Load() != 2 || len(requests) != 2 {
		t.Fatalf("suggestion changed the transcript or made %d calls", calls.Load())
	}
	prediction := requests[1]
	if len(prediction.Tools) == 0 || len(prediction.Messages) < 3 || !strings.Contains(prediction.Messages[0].Content, "Coddy") {
		t.Fatalf("prediction did not inherit the real turn's system prompt and tools")
	}
	if got := prediction.Messages[len(prediction.Messages)-1]; got.Role != llm.RoleUser || !strings.Contains(got.Content, "SUGGESTION MODE") {
		t.Fatalf("prediction instruction is not the trailing user message: %+v", got)
	}
	foundPrompt := false
	for _, msg := range prediction.Messages {
		if strings.Contains(msg.Content, "fix the bug and run the tests") {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		t.Fatal("prediction lost the original user request")
	}
}
