//go:build http

package httpserver

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// An assistant message the output limit cut off says so on the messages route,
// so that a client can draw it and the message that goes on from it as the one
// text they are (the web UI joins them on a reload, as it does while the answer
// streams). An answer a provider failure cut off does not say so: the step run
// again may start it over.
func TestLlmMsgsToCoddyOpenAIMarksATruncatedMessage(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "u"},
		{Role: llm.RoleAssistant, Content: "and then ", Truncated: true},
		{Role: llm.RoleAssistant, Content: "the rest."},
	})
	if len(out) != 3 {
		t.Fatalf("len=%d", len(out))
	}
	if got, _ := out[1]["truncated"].(bool); !got {
		t.Fatalf("a truncated message does not say so: %#v", out[1])
	}
	for _, i := range []int{0, 2} {
		if _, ok := out[i]["truncated"]; ok {
			t.Fatalf("message %d carries truncated: %#v", i, out[i])
		}
	}
	// The text goes out as stored, whitespace at the join included.
	if got, _ := out[1]["content"].(string); got != "and then " {
		t.Fatalf("content = %q", got)
	}
}

// TestOpenAPIDescribesATruncatedMessage: the schema that describes a message of
// the transcript (the one that has compaction_summary) has truncated beside it.
func TestOpenAPIDescribesATruncatedMessage(t *testing.T) {
	var found, described bool
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			if _, ok := x["compaction_summary"]; ok {
				found = true
				if p, ok := x["truncated"].(map[string]interface{}); ok && p["type"] == "boolean" {
					described = true
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []interface{}:
			for _, child := range x {
				walk(child)
			}
		}
	}
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	walk(doc)
	if !found || !described {
		t.Fatalf("message schema found = %v, truncated described = %v; want both", found, described)
	}
}
