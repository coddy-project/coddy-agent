// Package models is what the settings screen knows about a model the
// provider does not describe: its context window, a max_tokens to start from
// and whether it reads images, by model id, for the providers of models.dev a provider type of Coddy
// talks to. The table is models.json, built from models.dev
// (https://models.dev, MIT licence) by `make models-catalog` and embedded into
// the binary, so nothing is downloaded or read from disk at run time and no
// network request is needed to resolve a model at run time.
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

// SourceURL is where `make models-catalog` reads the catalog from.
const SourceURL = "https://models.dev/api.json"

// Path is models.json relative to the repository root.
const Path = "internal/llm/models/models.json"

//go:embed models.json
var snapshot []byte

// Limits are what the settings screen offers a models[] row.
type Limits struct {
	// ContextWindow is the total window shared by input and output.
	ContextWindow int
	InputLimit    int `json:"input_limit,omitempty"`
	OutputLimit   int `json:"output_limit,omitempty"`
	// MaxTokens is the answer's room; 0 when the catalog states none.
	MaxTokens int
	// Multimodal reports that the model reads images.
	Multimodal bool
}

// entry holds context, suggested output, image flag, input cap and output cap.
type entry [5]int

var (
	loadOnce sync.Once
	table    map[string]entry
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
	return Limits{ContextWindow: v[0], MaxTokens: v[1], Multimodal: v[2] == 1, InputLimit: v[3], OutputLimit: v[4]}, ok
}

// providers are the providers of models.dev a provider type of Coddy talks to
// (openai and codex: OpenAI's ids; opencode: the Zen and Go gateways), each
// marked when it is the vendor of its models: an id a vendor lists takes its
// numbers over a gateway's, which rounds or cuts them. NeuralDeep reports its
// windows itself and needs no catalog.
var providers = map[string]bool{
	"openai": true, "anthropic": true,
	"opencode": false, "opencode-go": false,
}

// Compact turns models.dev's api.json into models.json: one line per model id
// of the providers above, sorted, with its window, its max_tokens and whether
// it reads images (modalities.input names "image").
//
// Total context, input cap and output cap remain separate. MaxTokens is a
// conservative default for Settings, not the model's hard output limit.
// Vendor metadata wins; otherwise the smaller deployment window wins.
func Compact(api []byte) ([]byte, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Limit struct {
				Context int `json:"context"`
				Input   int `json:"input"`
				Output  int `json:"output"`
			} `json:"limit"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
		} `json:"models"`
	}
	if err := json.Unmarshal(api, &catalog); err != nil {
		return nil, fmt.Errorf("read models.dev catalog: %w", err)
	}
	type pick struct {
		limits entry
		vendor bool
	}
	picked := map[string]pick{}
	for provider, p := range catalog {
		vendor, ok := providers[provider]
		if !ok {
			continue
		}
		for id, m := range p.Models {
			window, out := m.Limit.Context, m.Limit.Output
			if in := m.Limit.Input; in > 0 && in < window {
				out = min(out, window-in)
			} else {
				out = min(out, window*(100-config.CompactionDefaultThresholdPercent)/100)
			}
			if window <= 0 || strings.TrimSpace(id) == "" {
				continue
			}
			image := 0
			if slices.Contains(m.Modalities.Input, "image") {
				image = 1
			}
			cur := pick{limits: entry{window, max(out, 0), image, m.Limit.Input, m.Limit.Output}, vendor: vendor}
			prev, seen := picked[id]
			if !seen || cur.vendor && !prev.vendor || cur.vendor == prev.vendor && smaller(cur.limits, prev.limits) {
				picked[id] = cur
			}
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("models.dev lists no model of OpenAI, Anthropic or OpenCode with a context window")
	}
	out := make(map[string]entry, len(picked))
	for id, p := range picked {
		out[id] = p.limits
	}
	return render(out), nil
}

// render writes the table as models.json: one model per line, sorted by id, so
// a refresh reads as a diff of the models that moved.
func render(table map[string]entry) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, id := range slices.Sorted(maps.Keys(table)) {
		key, _ := json.Marshal(id)
		fmt.Fprintf(&b, "%s:[%d,%d,%d,%d,%d]", key, table[id][0], table[id][1], table[id][2], table[id][3], table[id][4])
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
func smaller(a, b entry) bool {
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
	var a, b map[string]entry
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

// InputBudget reserves output (including reasoning tokens) within the resolved
// deployment window; catalog input limits may narrow it, never enlarge it.
func InputBudget(id string, window, output int) int {
	limits, known := Lookup(id)

	budget := max(0, window-max(0, output))
	if known && limits.InputLimit > 0 {
		budget = min(budget, limits.InputLimit)
	}
	return budget
}
