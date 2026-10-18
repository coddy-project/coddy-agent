//go:build http

package httpserver

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A summary row that continues the chain of summaries says so on the messages
// route, beside compaction_summary, so a client can tell the rows the model
// reads in turn from the one that starts the chain (issue #361).
func TestLlmMsgsToCoddyOpenAIMarksACompactionBlock(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "root", CompactionSummary: true},
		{Role: llm.RoleUser, Content: "block", CompactionSummary: true, CompactionBlock: true},
		{Role: llm.RoleUser, Content: "u"},
	})
	if len(out) != 3 {
		t.Fatalf("len=%d", len(out))
	}
	if got, _ := out[1]["compaction_block"].(bool); !got {
		t.Fatalf("a block does not say so: %#v", out[1])
	}
	for _, i := range []int{0, 2} {
		if _, ok := out[i]["compaction_block"]; ok {
			t.Fatalf("message %d carries compaction_block: %#v", i, out[i])
		}
	}
}

// The schema of a transcript message describes compaction_block beside
// compaction_summary.
func TestOpenAPIDescribesACompactionBlock(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var found, described bool
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			if _, ok := x["compaction_summary"]; ok {
				found = true
				if p, ok := x["compaction_block"].(map[string]interface{}); ok && p["type"] == "boolean" {
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
	walk(doc)
	if !found || !described {
		t.Fatalf("message schema found = %v, compaction_block described = %v; want both", found, described)
	}
}
