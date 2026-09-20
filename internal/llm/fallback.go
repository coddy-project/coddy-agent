package llm

import "context"

// FallbackCandidate is one model of a FallbackChain: the provider built for
// it and the models[].model id it was built from, for the log line.
type FallbackCandidate struct {
	Provider Provider
	Model    string
}

// fallbackChain tries its candidates in order and moves to the next one only
// when a call failed before producing any output. A model that is down,
// unauthorised or out of quota fails before its first byte and falls back; a
// stream that broke after text or a tool call was already delivered does not,
// because the caller has consumed that output and a second model would answer
// the same request twice. That is what the memory copilot's round-level
// fallback did (issue #247), minus the round streamed twice.
type fallbackChain struct {
	candidates []FallbackCandidate
	onFallback func(from, to string, err error)
}

// NewFallbackChain wraps candidates into one Provider. Entries without a
// provider are dropped; a chain of one is that provider itself. onFallback,
// when set, is told about every move to the next model.
func NewFallbackChain(candidates []FallbackCandidate, onFallback func(from, to string, err error)) Provider {
	kept := make([]FallbackCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Provider != nil {
			kept = append(kept, c)
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return kept[0].Provider
	}
	return &fallbackChain{candidates: kept, onFallback: onFallback}
}

func (c *fallbackChain) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	var lastErr error
	for i, cand := range c.candidates {
		resp, err := cand.Provider.Complete(ctx, messages, tools)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// A cancelled caller is not a failed model, and the last candidate has
		// nobody behind it.
		if ctx.Err() != nil || i+1 == len(c.candidates) {
			return nil, err
		}
		c.moved(cand.Model, c.candidates[i+1].Model, err)
	}
	return nil, lastErr
}

func (c *fallbackChain) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	var lastErr error
	for i, cand := range c.candidates {
		produced := false
		observe := func(ch StreamChunk) {
			if chunkHasOutput(ch) {
				produced = true
			}
			if onChunk != nil {
				onChunk(ch)
			}
		}
		resp, err := cand.Provider.Stream(ctx, messages, tools, observe)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if produced || ctx.Err() != nil || i+1 == len(c.candidates) {
			// The partial response travels with the error, as the provider
			// gave it: the loop keeps what was streamed.
			return resp, err
		}
		c.moved(cand.Model, c.candidates[i+1].Model, err)
	}
	return nil, lastErr
}

func (c *fallbackChain) moved(from, to string, err error) {
	if c.onFallback != nil {
		c.onFallback(from, to, err)
	}
}

// chunkHasOutput reports whether a chunk carried anything the caller can
// have acted on: text, reasoning, or a tool call, named or complete.
func chunkHasOutput(ch StreamChunk) bool {
	return ch.TextDelta != "" || ch.ReasoningDelta != "" || ch.ToolCall != nil || ch.ToolCallNamed != nil || ch.ToolCallDelta != nil
}
