// Package modelcatalog reads the catalog of model metadata the settings screen
// suggests models[] fields from: models.dev's api.json or a mirror of it. It
// keeps of the catalog only what a suggestion needs (the limits of a model and
// whether it reads images), matches a provider row to the catalog provider
// that serves its models, and turns an entry into the values a row is offered.
// A session never reads it: the runtime order of a context window stays
// max_context_tokens, the provider's listing, then the default.
package modelcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// maxLimit bounds a limit the catalog states: anything larger is a broken
// entry, not a model.
const maxLimit = 1 << 30

// Entry is what the catalog says of one model.
type Entry struct {
	// Context is limit.context: the whole window, prompt and answer.
	Context int `json:"context"`
	// Input is limit.input, 0 when the catalog states none: the part of the
	// window a prompt may take when input and output have budgets of their own.
	Input int `json:"input,omitempty"`
	// Output is limit.output: the most a model writes in one answer.
	Output int `json:"output,omitempty"`
	// Image reports that modalities.input names "image".
	Image bool `json:"image,omitempty"`
}

// Provider is one provider of the catalog.
type Provider struct {
	// API is the base URL the catalog names for the provider's
	// OpenAI-compatible (or Anthropic-compatible) endpoint, "" when it names
	// none.
	API    string           `json:"api,omitempty"`
	Models map[string]Entry `json:"models"`
}

// Index is the catalog by provider id.
type Index map[string]Provider

// Models counts the models of every provider.
func (idx Index) Models() int {
	n := 0
	for _, p := range idx {
		n += len(p.Models)
	}
	return n
}

type rawProvider struct {
	API    string                     `json:"api"`
	Models map[string]json.RawMessage `json:"models"`
}

// rawModel keeps each limit raw, so one broken limit costs only itself.
type rawModel struct {
	Limit struct {
		Context json.RawMessage `json:"context"`
		Input   json.RawMessage `json:"input"`
		Output  json.RawMessage `json:"output"`
	} `json:"limit"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

// Parse reads a catalog in the models.dev api.json shape. A model whose entry
// does not decode, or states no usable window, is left out on its own; a
// document that is not an object of providers is an error.
func Parse(r io.Reader) (Index, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("models catalog: not a catalog document: %w", err)
	}
	idx := make(Index, len(raw))
	for id, body := range raw {
		var p rawProvider
		if json.Unmarshal(body, &p) != nil {
			continue
		}
		models := make(map[string]Entry, len(p.Models))
		for mid, mbody := range p.Models {
			if e, ok := parseModel(mbody); ok {
				models[mid] = e
			}
		}
		if len(models) == 0 {
			continue
		}
		idx[id] = Provider{API: p.API, Models: models}
	}
	return idx, nil
}

func parseModel(body json.RawMessage) (Entry, bool) {
	var m rawModel
	if json.Unmarshal(body, &m) != nil {
		return Entry{}, false
	}
	e := Entry{
		Context: limit(m.Limit.Context),
		Input:   limit(m.Limit.Input),
		Output:  limit(m.Limit.Output),
		Image:   slices.Contains(m.Modalities.Input, "image"),
	}
	if e.Context == 0 {
		return Entry{}, false
	}
	// An input part larger than the whole window says nothing true.
	if e.Input > e.Context {
		e.Input = 0
	}
	return e, true
}

// limit reads one stated limit, 0 for a missing, non-numeric, fractional,
// negative or absurd one.
func limit(raw json.RawMessage) int {
	var n json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil {
		return 0
	}
	v, err := n.Int64()
	if err != nil || v <= 0 || v > maxLimit {
		return 0
	}
	return int(v)
}
