package session

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func newSuggestionTestManager(t *testing.T, respond func(http.ResponseWriter, *http.Request)) (*Manager, *State, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		respond(w, r)
	}))
	t.Cleanup(server.Close)
	zero := 0
	blocking := false
	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir()},
		Providers: []config.ProviderConfig{{Name: "p", Type: "openai", APIBase: server.URL, APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "p/test", Stream: &blocking}},
		Agent:     config.Agent{Model: "p/test", LLMRetryMax: &zero},
	}
	st := &State{ID: "sess_suggestion"}
	for _, text := range []string{"fix the bug", "fixed the bug", "add a test", "added the test"} {
		role := llm.RoleUser
		if len(st.GetMessages())%2 == 1 {
			role = llm.RoleAssistant
		}
		st.AddMessage(llm.Message{Role: role, Content: text})
	}
	m := &Manager{sessions: map[string]*State{st.ID: st}}
	m.cfgAt.Store(cfg)
	return m, st, &calls
}

func answerSuggestion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": "run the tests"}, "finish_reason": "stop"}},
	})
}

func TestPromptSuggestionCanceledCallDoesNotCacheNextRevision(t *testing.T) {
	started := make(chan struct{})
	m, st, calls := newSuggestionTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
			answerSuggestion(w, r)
		default:
			close(started)
			<-r.Context().Done()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = m.GeneratePromptSuggestion(ctx, st.ID)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	m.CancelPromptSuggestion(st.ID)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "check another file"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "checked it"})
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	text, err := m.GeneratePromptSuggestion(ctx, st.ID)
	if err != nil || text != "run the tests" || calls.Load() != 2 {
		t.Fatalf("next revision: text=%q err=%v calls=%d", text, err, calls.Load())
	}
}

func TestPromptSuggestionFailedCallCanRetry(t *testing.T) {
	var failed atomic.Bool
	m, st, calls := newSuggestionTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if !failed.Swap(true) {
			http.Error(w, "temporary failure", http.StatusBadRequest)
			return
		}
		answerSuggestion(w, r)
	})
	if _, err := m.GeneratePromptSuggestion(context.Background(), st.ID); err == nil {
		t.Fatal("expected provider failure")
	}
	text, err := m.GeneratePromptSuggestion(context.Background(), st.ID)
	if err != nil || text != "run the tests" || calls.Load() != 2 {
		t.Fatalf("retry: text=%q err=%v calls=%d", text, err, calls.Load())
	}
}

func TestPromptSuggestionConcurrentReadersShareCall(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	m, st, calls := newSuggestionTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			answerSuggestion(w, r)
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := m.GeneratePromptSuggestion(firstCtx, st.ID); first <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second := make(chan string, 1)
	go func() {
		text, _ := m.GeneratePromptSuggestion(ctx, st.ID)
		second <- text
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		m.promptSuggestionSide.mu.Lock()
		call := m.promptSuggestionSide.call[st.ID]
		ready := call != nil && call.waiters == 2
		m.promptSuggestionSide.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("the readers did not join the same call")
		}
	}
	cancelFirst()
	if err := <-first; err != context.Canceled {
		t.Fatalf("first reader: %v", err)
	}
	once.Do(func() { close(release) })
	if text := <-second; text != "run the tests" || calls.Load() != 1 {
		t.Fatalf("second reader: %q, provider calls %d", text, calls.Load())
	}
}

func TestPromptSuggestionDisableRetiresCachedAnswer(t *testing.T) {
	m, st, calls := newSuggestionTestManager(t, answerSuggestion)
	if _, err := m.GeneratePromptSuggestion(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	next := *m.activeCfg()
	off := false
	next.Agent.PromptSuggestions = &off
	m.cfgAt.Store(&next)
	m.resetPromptSuggestions()
	if text, err := m.GeneratePromptSuggestion(context.Background(), st.ID); text != "" || !IsPromptSuggestionSuppressed(err) {
		t.Fatalf("disabled suggestion: %q, %v", text, err)
	}
	m.NotePromptSuggestionOutcome(st.ID, false)
	if state := (&promptSuggestionStore{home: next.Paths.Home}).load(); state.UnusedStreak != 0 {
		t.Fatalf("disabled feature recorded an outcome: %+v", state)
	}
	if calls.Load() != 1 {
		t.Fatalf("disabled feature called the model: %d", calls.Load())
	}
}

func TestValidatePromptSuggestionAcceptsPlainLines(t *testing.T) {
	for _, in := range []string{
		"run the tests",
		"yes",
		"да",
		"продолжай",
		"push it",
		"/resume",
		"  try it out  ",
		"<suggestion>commit this</suggestion>",
		"Suggested response: run the tests",
	} {
		if got, category := ValidatePromptSuggestion(in); category != "" {
			t.Errorf("ValidatePromptSuggestion(%q) refused with %q (got %q)", in, category, got)
		}
	}
}

func TestValidatePromptSuggestionRefusesCategories(t *testing.T) {
	cases := []struct {
		in       string
		category string
	}{
		{"done", "done"},
		{"Nothing to suggest here", "meta_text"},
		{"Silence is golden", "meta_text"},
		{"(nothing here)", "meta_wrapped"},
		{"api error: boom", "error_message"},
		{"Label: run the tests", "prefixed_label"},
		{"run", "too_few_words"},
		{"one two three four five six seven eight nine ten eleven twelve thirteen", "too_many_words"},
		// 2 words, but past the length cap: the length is checked after the
		// word count, like Claude Code's order.
		{strings.Repeat("a", 99) + " b", "too_long"},
		{"First sentence. Second one", "multiple_sentences"},
		{"bold *stuff*", "has_formatting"},
		{"thanks, that works", "evaluative"},
		{"Let me check the file", "agent_voice"},
		{"", "empty"},
	}
	for _, c := range cases {
		if _, category := ValidatePromptSuggestion(c.in); category != c.category {
			t.Errorf("ValidatePromptSuggestion(%q) refused with %q, want %q", c.in, category, c.category)
		}
	}
}

func TestPromptSuggestionStateBackOff(t *testing.T) {
	fresh := promptSuggestionState{FirstStart: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), UnusedStreak: promptSuggestionBackOffAfter}
	if fresh.backedOff(time.Now()) {
		t.Fatal("a fresh install never backs off, whatever the streak")
	}
	old := promptSuggestionState{FirstStart: time.Now().UTC().Add(-promptSuggestionGrace - time.Hour).Format(time.RFC3339)}
	if old.backedOff(time.Now()) {
		t.Fatal("no back-off before the streak reaches the threshold")
	}
	old.UnusedStreak = promptSuggestionBackOffAfter
	if !old.backedOff(time.Now()) {
		t.Fatal("the threshold past the grace period backs off")
	}
}

func TestPromptSuggestionStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := &promptSuggestionStore{home: dir}
	store.update(func(s *promptSuggestionState) { s.UnusedStreak = 3 })
	reloaded := (&promptSuggestionStore{home: dir}).load()
	if reloaded.UnusedStreak != 3 || reloaded.FirstStart == "" {
		t.Fatalf("store round trip lost the streak or the start: %+v", reloaded)
	}
	store.update(func(s *promptSuggestionState) { s.UnusedStreak = 0 })
	if got := (&promptSuggestionStore{home: dir}).load().UnusedStreak; got != 0 {
		t.Fatalf("accepted outcome resets the streak, got %d", got)
	}
}

func TestPromptSuggestionHistoryWindow(t *testing.T) {
	var msgs []llm.Message
	for i := 0; i < promptSuggestionHistoryMessages+6; i++ {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		msgs = append(msgs, llm.Message{Role: role, Content: strings.Repeat("x", 50)})
	}
	window := promptSuggestionHistoryWindow(msgs)
	if len(window) != promptSuggestionHistoryMessages {
		t.Fatalf("window holds %d messages, want %d", len(window), promptSuggestionHistoryMessages)
	}
	// A tool result inside the window is skipped, not counted.
	withTool := append(append([]llm.Message(nil), msgs...), llm.Message{Role: llm.RoleTool, Content: "tool output"})
	if got := len(promptSuggestionHistoryWindow(withTool)); got != promptSuggestionHistoryMessages-1 {
		t.Fatalf("a tool result must shrink the window, got %d", got)
	}
	long := promptSuggestionHistoryWindow([]llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("y", promptSuggestionMessageRunes*2)}})
	if len([]rune(long[0].Content)) > promptSuggestionMessageRunes+2 {
		t.Fatalf("a message longer than the cap was not clipped: %d runes", len([]rune(long[0].Content)))
	}
}
