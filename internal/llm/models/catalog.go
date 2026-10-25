// Package models is what the settings screen knows about a model the
// provider does not describe: its context window and a max_tokens to start
// from, by model id. The table is models.json, built from models.dev
// (https://models.dev, MIT licence) by `make models-catalog` and embedded into
// the binary, so nothing is downloaded or read from disk at run time and no
// session ever reads it: it only fills the forms of the settings screen.
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

// Snapshot is models.json as embedded, which the site publishes as
// coddy.dev/models.json.
func Snapshot() []byte { return snapshot }

// vendors are the providers of models.dev that serve their own models. When
// one of them lists an id, its numbers win over those of resellers and
// gateways, which round or cut them.
var vendors = map[string]bool{
	"openai": true, "anthropic": true, "google": true, "mistral": true,
	"deepseek": true, "xai": true, "moonshotai": true, "zai": true, "alibaba": true,
}

// Compact turns models.dev's api.json into models.json: one line per model id,
// sorted, with its window and max_tokens.
//
// The window is limit.input when the catalog states one (GPT-5: 272000 of a
// 400000 window that also holds the answer), else limit.context. max_tokens is
// the whole limit.output for a model with an input budget of its own; a model
// whose prompt and answer share the window gets at most the part above the
// default compaction threshold, a fifth of it at 80%, since a prompt grows to
// the threshold before a compaction and a provider refuses a request whose
// prompt and max_tokens do not fit the window together. An id listed by
// several providers takes the vendor's numbers when a vendor lists it, else
// the smallest of all: a window larger than the real one is worse than none.
func Compact(api []byte) ([]byte, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Limit struct {
				Context int `json:"context"`
				Input   int `json:"input"`
				Output  int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(api, &catalog); err != nil {
		return nil, fmt.Errorf("read models.dev catalog: %w", err)
	}
	type pick struct {
		limits [2]int
		vendor bool
	}
	picked := map[string]pick{}
	for provider, p := range catalog {
		for id, m := range p.Models {
			window, out := m.Limit.Context, m.Limit.Output
			if m.Limit.Input > 0 {
				window = m.Limit.Input
			} else {
				out = min(out, window*(100-config.CompactionDefaultThresholdPercent)/100)
			}
			if window <= 0 || strings.TrimSpace(id) == "" {
				continue
			}
			cur := pick{limits: [2]int{window, max(out, 0)}, vendor: vendors[provider]}
			prev, seen := picked[id]
			switch {
			case !seen || cur.vendor && !prev.vendor:
				picked[id] = cur
			case cur.vendor == prev.vendor:
				picked[id] = pick{limits: [2]int{min(prev.limits[0], window), minKnown(prev.limits[1], cur.limits[1])}, vendor: cur.vendor}
			}
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("models.dev catalog lists no model with a context window")
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

// minKnown is the smaller of two max_tokens, where 0 means unknown.
func minKnown(a, b int) int {
	if a == 0 || b == 0 {
		return max(a, b)
	}
	return min(a, b)
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
