// Package models is what the settings screen knows about the models of the
// providers Coddy has: a context window and a max_tokens to start from, by
// model id. The table is models.json, built by `make models-catalog` from
// models.dev (https://models.dev, MIT licence) for OpenAI, Anthropic and
// OpenCode and from NeuralDeep's own model list, and embedded into the binary,
// so nothing is downloaded or read from disk at run time and no session ever
// reads it: it only fills the forms of the settings screen.
package models

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// SourceURL is where `make models-catalog` reads models.dev's catalog from.
const SourceURL = "https://models.dev/api.json"

// Path is models.json relative to the repository root.
const Path = "internal/llm/models/models.json"

//go:embed models.json
var snapshot []byte

// Limits are the numbers the settings screen offers a models[] row.
type Limits struct {
	// ContextWindow is the part of the window a prompt may take.
	ContextWindow int
	// MaxTokens is the answer's room; 0 when the catalog states none.
	MaxTokens int
}

var (
	loadOnce sync.Once
	table    map[string][2]int
)

// Lookup returns the limits of the model with exactly this id.
func Lookup(id string) (Limits, bool) {
	loadOnce.Do(func() {
		// The snapshot is checked by the tests and by the command that writes
		// it, so a table that does not parse is a build that cannot ship.
		if err := json.Unmarshal(snapshot, &table); err != nil {
			panic("models: embedded models.json: " + err.Error())
		}
	})
	v, ok := table[id]
	return Limits{ContextWindow: v[0], MaxTokens: v[1]}, ok
}

// providers are the providers of models.dev a provider type of Coddy talks to
// (openai and codex: OpenAI's ids; opencode: the Zen and Go gateways), each
// marked when it is the vendor of its models: an id a vendor lists takes its
// numbers over a gateway's, which rounds or cuts them. NeuralDeep, which
// models.dev does not carry, is read from its own model list and is no
// vendor: it serves other makers' models, and its listing reports its windows
// itself, so on an id it shares with a gateway the smaller window wins.
var providers = map[string]bool{
	"openai": true, "anthropic": true,
	"opencode": false, "opencode-go": false,
	neuralDeep: false,
}

const neuralDeep = "neuraldeep"

type limit struct {
	Context int `json:"context"`
	Input   int `json:"input"`
	Output  int `json:"output"`
}

// Compact turns models.dev's api.json and the answer of NeuralDeep's GET
// /v1/models into models.json: one line per model id, sorted, with its window
// and max_tokens. Of models.dev it keeps the providers above; of NeuralDeep the
// chat models, whose limits come in models.dev's shape.
//
// The window is limit.input when the catalog states one below limit.context
// (GPT-5: 272000 of a 400000 window that also holds the answer), else
// limit.context. max_tokens is the whole limit.output for such a model with an
// input budget of its own; a model whose prompt and answer share the window
// (limit.input absent, or as large as the window) gets at most the part above the
// default compaction threshold, a fifth of it at 80%, since a prompt grows to
// the threshold before a compaction and a provider refuses a request whose
// prompt and max_tokens do not fit the window together. An id listed by
// several providers takes the vendor's numbers when a vendor lists it, else
// the pair with the smallest window: a window larger than the real one is
// worse than none, and a pair is never split, since its max_tokens was worked
// out for its own window.
func Compact(modelsDev, neuralDeepModels []byte) ([]byte, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Limit limit `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(modelsDev, &catalog); err != nil {
		return nil, fmt.Errorf("read models.dev catalog: %w", err)
	}
	var nd struct {
		Data []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Limit limit  `json:"limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(neuralDeepModels, &nd); err != nil {
		return nil, fmt.Errorf("read NeuralDeep model list: %w", err)
	}
	limits := map[string]map[string]limit{}
	for provider, p := range catalog {
		if _, ok := providers[provider]; !ok {
			continue
		}
		limits[provider] = map[string]limit{}
		for id, m := range p.Models {
			limits[provider][id] = m.Limit
		}
	}
	limits[neuralDeep] = map[string]limit{}
	for _, m := range nd.Data {
		if m.Type == "chat" {
			limits[neuralDeep][m.ID] = m.Limit
		}
	}
	type pick struct {
		limits [2]int
		vendor bool
	}
	picked := map[string]pick{}
	kept := map[bool]int{} // models kept, by whether NeuralDeep listed them
	for provider, models := range limits {
		for id, l := range models {
			window, out := l.Context, l.Output
			if in := l.Input; in > 0 && in < window {
				window = in
			} else {
				out = min(out, window*(100-config.CompactionDefaultThresholdPercent)/100)
			}
			if window <= 0 || strings.TrimSpace(id) == "" {
				continue
			}
			kept[provider == neuralDeep]++
			cur := pick{limits: [2]int{window, max(out, 0)}, vendor: providers[provider]}
			prev, seen := picked[id]
			if !seen || cur.vendor && !prev.vendor || cur.vendor == prev.vendor && smaller(cur.limits, prev.limits) {
				picked[id] = cur
			}
		}
	}
	// Either source answering with nothing usable is an outage or a format
	// change, never a reason to drop its models from the table.
	if kept[false] == 0 {
		return nil, fmt.Errorf("models.dev lists no model of OpenAI, Anthropic or OpenCode with a context window")
	}
	if kept[true] == 0 {
		return nil, fmt.Errorf("NeuralDeep lists no chat model with a context window")
	}
	out := make(map[string][2]int, len(picked))
	for id, p := range picked {
		out[id] = p.limits
	}
	return render(out), nil
}

// render writes the table as models.json: one model per line, sorted by id, so
// a refresh reads as a diff of the models that moved.
func render(table map[string][2]int) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, id := range slices.Sorted(maps.Keys(table)) {
		key, _ := json.Marshal(id)
		fmt.Fprintf(&b, "%s:[%d,%d]", key, table[id][0], table[id][1])
		if i < len(table)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// smaller orders two [window, max_tokens] pairs: the smaller window first, then
// the smaller max_tokens, where 0 (unknown) counts as the largest.
func smaller(a, b [2]int) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	known := func(n int) int {
		if n == 0 {
			return int(^uint(0) >> 1)
		}
		return n
	}
	return known(a[1]) < known(b[1])
}

// Changes says how next differs from prev, both in the models.json format:
// the counts and the first ids of each kind.
func Changes(prev, next []byte) (string, error) {
	var a, b map[string][2]int
	if len(prev) > 0 {
		if err := json.Unmarshal(prev, &a); err != nil {
			return "", fmt.Errorf("read the current models.json: %w", err)
		}
	}
	if err := json.Unmarshal(next, &b); err != nil {
		return "", err
	}
	var added, removed, changed []string
	for id, v := range b {
		if old, ok := a[id]; !ok {
			added = append(added, id)
		} else if old != v {
			changed = append(changed, id)
		}
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			removed = append(removed, id)
		}
	}
	var s strings.Builder
	fmt.Fprintf(&s, "%d models: %d added, %d removed, %d changed\n", len(b), len(added), len(removed), len(changed))
	for _, group := range []struct {
		name string
		ids  []string
	}{{"added", added}, {"removed", removed}, {"changed", changed}} {
		if len(group.ids) == 0 {
			continue
		}
		slices.Sort(group.ids)
		if len(group.ids) > 10 {
			group.ids = append(group.ids[:10], "...")
		}
		fmt.Fprintf(&s, "  %s: %s\n", group.name, strings.Join(group.ids, ", "))
	}
	return s.String(), nil
}
