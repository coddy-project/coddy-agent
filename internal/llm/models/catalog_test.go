package models

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A trimmed api.json of models.dev: the shapes Compact has to tell apart.
const sampleCatalog = `{
  "anthropic": {"models": {
    "claude-sonnet-4-5": {"limit": {"context": 200000, "output": 64000}}
  }},
  "openai": {"models": {
    "gpt-5": {"limit": {"context": 400000, "input": 272000, "output": 128000}},
    "input-is-window": {"limit": {"context": 8192, "input": 8192, "output": 26214}}
  }},
  "opencode": {"models": {
    "claude-sonnet-4-5": {"limit": {"context": 1000000, "output": 64000}},
    "kimi-k2": {"limit": {"context": 262144, "output": 262144}},
    "split": {"limit": {"context": 131072, "output": 131072}},
    "tie": {"limit": {"context": 64000, "output": 8000}}
  }},
  "opencode-go": {"models": {
    "kimi-k2": {"limit": {"context": 128000, "output": 16384}},
    "no-output": {"limit": {"context": 32000}},
    "split": {"limit": {"context": 8192}},
    "tie": {"limit": {"context": 64000, "output": 4000}}
  }},
  "google": {"models": {
    "gemini-2.5-pro": {"limit": {"context": 1048576, "output": 65536}}
  }}
}`

func TestCompact(t *testing.T) {
	got, err := Compact([]byte(sampleCatalog))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"{",
		// The vendor's numbers win over a gateway's; the answer keeps to the
		// fifth of a shared window above the compaction threshold.
		`"claude-sonnet-4-5":[200000,40000],`,
		// A model with an input budget: the window is that budget and the
		// answer has its whole output limit.
		`"gpt-5":[272000,128000],`,
		// An input limit as large as the window is no budget of its own: the
		// answer shares the window and keeps to its fifth.
		`"input-is-window":[8192,1638],`,
		// Only gateways: the pair with the smallest window.
		`"kimi-k2":[128000,16384],`,
		// No output limit: no max_tokens to offer.
		`"no-output":[32000,0],`,
		// The smaller window wins with its own max_tokens: never the window
		// of one provider and the max_tokens of another.
		`"split":[8192,0],`,
		// One window: the smaller max_tokens.
		`"tie":[64000,4000]`,
		// gemini-2.5-pro is left out: Coddy has no Google provider.
		"}",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("Compact =\n%s\nwant\n%s", got, want)
	}
}

func TestCompactRefusesAnEmptyCatalog(t *testing.T) {
	for _, api := range []string{`{}`, `{"openai": {"models": {}}}`, `{"google": {"models": {"g": {"limit": {"context": 1000}}}}}`, `not json`} {
		if _, err := Compact([]byte(api)); err == nil {
			t.Errorf("Compact(%s) succeeded", api)
		}
	}
}

// The embedded table is what Compact writes: a hand edit or a merge that
// breaks its order or format shows here, not in a settings form.
func TestSnapshotIsCanonical(t *testing.T) {
	var table map[string][2]int
	if err := json.Unmarshal(snapshot, &table); err != nil {
		t.Fatalf("models.json: %v", err)
	}
	if len(table) < 100 {
		t.Fatalf("models.json holds %d models", len(table))
	}
	if !bytes.Equal(render(table), snapshot) {
		t.Fatal("models.json is not in the format make models-catalog writes; run it again")
	}
	for id, v := range table {
		if v[0] <= 0 || v[1] < 0 {
			t.Errorf("%s: [%d,%d] is not a window and a max_tokens", id, v[0], v[1])
		}
	}
}

func TestLookup(t *testing.T) {
	var table map[string][2]int
	if err := json.Unmarshal(snapshot, &table); err != nil {
		t.Fatal(err)
	}
	for id, v := range table {
		got, ok := Lookup(id)
		if !ok || got.ContextWindow != v[0] || got.MaxTokens != v[1] {
			t.Fatalf("Lookup(%q) = %+v, %v; want %v", id, got, ok, v)
		}
		break
	}
	if _, ok := Lookup("no-such-model-in-any-catalog"); ok {
		t.Error("Lookup found a model the table does not list")
	}
}

func TestChanges(t *testing.T) {
	prev := []byte(`{"a":[1,1],"b":[2,2],"c":[3,3]}`)
	next := []byte(`{"a":[1,1],"b":[2,9],"d":[4,4]}`)
	got, err := Changes(prev, next)
	if err != nil {
		t.Fatal(err)
	}
	want := "3 models: 1 added, 1 removed, 1 changed\n  added: d\n  removed: c\n  changed: b\n"
	if got != want {
		t.Fatalf("Changes =\n%s\nwant\n%s", got, want)
	}
}
