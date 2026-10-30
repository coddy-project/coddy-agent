package session

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestValidatePromptSuggestionAcceptsPlainLines(t *testing.T) {
	for _, in := range []string{
		"run the tests",
		"yes",
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
