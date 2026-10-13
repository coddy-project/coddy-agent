//go:build cli

package cli

// Edges of the question modal (modals.go). The happy path - a typed answer on
// a question that allows one - is in features/cli_tui.feature.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// keyDown is declared in goal_test.go.
const (
	keyEnter = "\r"
	keySpace = " "
)

func newTestQuestionModal(q acp.QuestionPrompt, got **acp.QuestionResult) *questionModal {
	m := newQuestionModal(newTheme("dark"), acp.QuestionRequestParams{
		SessionID: "sess_modal",
		RequestID: "q_modal",
		Questions: []acp.QuestionPrompt{q},
	}, nil)
	m.OnDone = func(res *acp.QuestionResult) { *got = res }
	return m
}

func runQuestionModal(t *testing.T, q acp.QuestionPrompt, keys ...string) [][]string {
	t.Helper()
	var got *acp.QuestionResult
	m := newTestQuestionModal(q, &got)
	for _, k := range keys {
		m.HandleInput([]byte(k))
	}
	if got == nil {
		t.Fatalf("the modal never answered after %q", keys)
	}
	return got.Answers
}

// A multiple-choice question keeps the options toggled with space when the
// operator also types an answer of their own, as the web card does: the typed
// text joins them rather than replacing them.
func TestQuestionModalOwnAnswerJoinsTheToggledOptions(t *testing.T) {
	q := acp.QuestionPrompt{
		Question: "Which checks?",
		Options:  []acp.QuestionOption{{Label: "lint"}, {Label: "tests"}},
		Multiple: true,
		Custom:   true,
	}
	got := runQuestionModal(t, q,
		keySpace,         // toggle "lint"
		keyDown, keyDown, // to "Custom answer..."
		keyEnter, // open the editor
		"a", " ", "s", "m", "o", "k", "e",
		keyEnter,
	)
	want := [][]string{{"lint", "a smoke"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("answers %q, want %q", got, want)
	}
}

// An own answer left empty is no answer: a multiple-choice question returns
// the toggled options alone, a single-choice one returns nothing rather than
// an empty string the model would read as text.
func TestQuestionModalEmptyOwnAnswerAddsNothing(t *testing.T) {
	multi := acp.QuestionPrompt{
		Question: "Which checks?",
		Options:  []acp.QuestionOption{{Label: "lint"}, {Label: "tests"}},
		Multiple: true,
		Custom:   true,
	}
	if got := runQuestionModal(t, multi, keySpace, keyDown, keyDown, keyEnter, " ", keyEnter); !reflect.DeepEqual(got, [][]string{{"lint"}}) {
		t.Fatalf("multiple choice answers %q, want only the toggled option", got)
	}
	single := acp.QuestionPrompt{
		Question: "Which database?",
		Options:  []acp.QuestionOption{{Label: "postgres"}},
		Custom:   true,
	}
	got := runQuestionModal(t, single, keyDown, keyEnter, keyEnter)
	// The model reads the answers as JSON: an empty row is [], never null, the
	// shape the web card sends for a question left without an answer.
	raw, err := json.Marshal(acp.QuestionResult{Answers: got})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"answers":[[]]}` {
		t.Fatalf("single choice answers encode as %s, want one empty row", raw)
	}
}

// A question that turned custom off lists its options and nothing else.
func TestQuestionModalWithoutCustomListsOnlyTheOptions(t *testing.T) {
	var got *acp.QuestionResult
	m := newTestQuestionModal(acp.QuestionPrompt{
		Question: "Delete the branch?",
		Options:  []acp.QuestionOption{{Label: "yes"}, {Label: "no"}},
	}, &got)
	// The cursor wraps, so a few steps down visit every row.
	rows := map[string]bool{}
	for i := 0; i < 5; i++ {
		it := m.list.SelectedItem()
		if it == nil {
			t.Fatal("the list has no rows")
		}
		if it.Value == customAnswerValue {
			t.Fatalf("a question without custom offers %q", it.Label)
		}
		rows[it.Value] = true
		m.list.MoveDown()
	}
	if len(rows) != 2 {
		t.Fatalf("the list has rows %v, want the 2 options", rows)
	}
}
