package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

// A question offers the user's own answer unless it turns that off (issue
// #257): a missing custom key decodes as true, an explicit false stays false.
func TestQuestionPromptCustomDefaultsToTrue(t *testing.T) {
	cases := []struct {
		doc  string
		want bool
	}{
		{`{"question":"Q","options":[{"label":"A"}]}`, true},
		{`{"question":"Q","options":[{"label":"A"}],"custom":true}`, true},
		{`{"question":"Q","options":[{"label":"A"}],"custom":false}`, false},
	}
	for _, c := range cases {
		var q QuestionPrompt
		if err := json.Unmarshal([]byte(c.doc), &q); err != nil {
			t.Fatalf("%s: %v", c.doc, err)
		}
		if q.Custom != c.want || q.Question != "Q" || len(q.Options) != 1 {
			t.Fatalf("%s decoded as %+v, want custom=%v", c.doc, q, c.want)
		}
	}
	var list []QuestionPrompt
	if err := json.Unmarshal([]byte(`[{"question":"A","options":[]},{"question":"B","options":[],"custom":false}]`), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Custom || list[1].Custom {
		t.Fatalf("list decoded as %+v", list)
	}
}

// The key is always written, so a client tells a question that turned the
// answer off from one that never mentioned it.
func TestQuestionPromptWritesCustomEvenWhenFalse(t *testing.T) {
	raw, err := json.Marshal(QuestionPrompt{Question: "Q", Options: []QuestionOption{{Label: "A"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"custom":false`) {
		t.Fatalf("encoded %s, want an explicit custom:false", raw)
	}
	var back QuestionPrompt
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Custom {
		t.Fatalf("a question that turned the answer off came back with it on: %s", raw)
	}
}
