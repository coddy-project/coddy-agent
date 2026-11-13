package session

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The suggestion is a second request over the turn's own context, so what it
// costs is decided by where the provider's cache marks land and whether its
// thinking parameters still match the turn's. Both are read off the wire of
// the Anthropic provider, which is the one that takes the marks explicitly.
//
// The mark must sit on the turn's last real message - the prefix the
// operator's next turn starts from - and never on the suggestion instruction,
// which no later request repeats. The output bound is lowered only where the
// turn asked for no reasoning: on Anthropic the extended-thinking budget is a
// fraction of max_tokens, and a request whose thinking parameters differ from
// the turn's reads none of the message blocks the turn cached.
func TestPromptCacheSuggestionReusesTheTurnsPrefix(t *testing.T) {
	const answer = "The fix is in session.go."
	cases := []struct {
		name          string
		reasoning     string
		wantMaxTokens int64
		wantThinking  bool
	}{
		{name: "no reasoning", reasoning: "", wantMaxTokens: promptSuggestionMaxTokens},
		{name: "reasoning off", reasoning: config.ReasoningOff, wantMaxTokens: promptSuggestionMaxTokens},
		{name: "reasoning on", reasoning: "high", wantMaxTokens: 8192, wantThinking: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg","type":"message","role":"assistant","model":"model","content":[{"type":"text","text":"run the tests"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)
			}))
			t.Cleanup(server.Close)

			blocking := false
			cfg := &config.Config{
				Paths:     config.Paths{Home: t.TempDir()},
				Providers: []config.ProviderConfig{{Name: "a", Type: "anthropic", APIBase: server.URL, APIKey: "test", Proxy: "none"}},
				Models:    []config.ModelEntry{{Model: "a/model", MaxTokens: 8192, Stream: &blocking}},
				Agent:     config.Agent{Model: "a/model"},
			}
			st := &State{ID: "sess_suggestion_cache"}
			st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "fix the bug"})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: answer})
			fork := append([]llm.Message{{Role: llm.RoleSystem, Content: strings.Repeat("You are Coddy. ", 200)}}, MessagesForLLM(st.GetMessages())...)
			st.SetPromptSuggestionFork(fork, nil, llm.ProviderInput{
				Name: "a", Type: "anthropic", Model: "model", APIKey: "test",
				BaseURL: server.URL, ProxyURL: "none", MaxTokens: 8192,
				ReasoningEffort: tc.reasoning, DisableStream: true, RetryDisabled: true,
			})
			m := &Manager{sessions: map[string]*State{st.ID: st}}
			m.cfgAt.Store(cfg)

			if text, err := m.GeneratePromptSuggestion(context.Background(), st.ID); err != nil || text != "run the tests" {
				t.Fatalf("suggestion = %q, %v", text, err)
			}

			// The turn's two messages, then the instruction.
			if n := gjson.Get(body, "messages.#").Int(); n != 3 {
				t.Fatalf("request holds %d messages, want the turn's two and the instruction", n)
			}
			last := gjson.Get(body, "messages.2.content.0.text").String()
			if !strings.Contains(last, "SUGGESTION MODE") {
				t.Fatalf("the last message is not the instruction: %q", last)
			}
			if gjson.Get(body, "messages.2.content.0.cache_control").Exists() {
				t.Fatal("the instruction carries the cache mark: the side call would write a prefix no later request repeats")
			}
			if got := gjson.Get(body, "messages.1.content.@reverse.0.cache_control.type").String(); got != "ephemeral" {
				t.Fatalf("cache mark on the turn's answer = %q, want ephemeral", got)
			}
			if got := gjson.Get(body, "messages.1.content.0.text").String(); got != answer {
				t.Fatalf("the marked message is not the turn's answer: %q", got)
			}
			if got := gjson.Get(body, "system.0.cache_control.type").String(); got != "ephemeral" {
				t.Fatalf("system prompt cache mark = %q, want ephemeral", got)
			}
			if n := strings.Count(body, `"cache_control"`); n != 2 {
				t.Fatalf("%d cache marks, want 2 (the system prompt and the turn's tail)", n)
			}
			if got := gjson.Get(body, "max_tokens").Int(); got != tc.wantMaxTokens {
				t.Fatalf("max_tokens = %d, want %d", got, tc.wantMaxTokens)
			}
			if got := gjson.Get(body, "thinking").Exists(); got != tc.wantThinking {
				t.Fatalf("thinking present = %v, want %v: the turn's thinking parameters decide whether its message cache is read", got, tc.wantThinking)
			}
			if tc.wantThinking {
				// What the turn itself sends at this model and level: a budget
				// that follows max_tokens, which is why the bound stays put.
				if got := gjson.Get(body, "thinking.budget_tokens").Int(); got != 6144 {
					t.Fatalf("thinking.budget_tokens = %d, want the turn's 6144", got)
				}
			}
		})
	}
}

// A fork from before the turn's provider input was kept - or one a surface
// built by hand - has no request to repeat, and a hand-built one would miss
// the cache. Such a turn gets no suggestion rather than a cold call.
func TestPromptSuggestionWithoutTheTurnsInputIsSuppressed(t *testing.T) {
	m, st, calls := newSuggestionTestManager(t, answerSuggestion)
	messages := append([]llm.Message{{Role: llm.RoleSystem, Content: "MAIN_SYSTEM_PROMPT"}}, MessagesForLLM(st.GetMessages())...)
	st.SetPromptSuggestionFork(messages, nil, llm.ProviderInput{})
	text, err := m.GeneratePromptSuggestion(context.Background(), st.ID)
	if text != "" || !IsPromptSuggestionSuppressed(err) {
		t.Fatalf("fork without a provider input: text=%q err=%v", text, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("fork without a provider input reached the provider %d times", calls.Load())
	}
}
