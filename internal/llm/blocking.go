package llm

import "context"

// blockingProvider serves Provider.Stream without opening a stream: the inner
// provider runs one blocking Complete call and the finished response is replayed
// through onChunk. It exists for models configured with stream: false, where the
// backend or a proxy in front of it handles SSE badly.
//
// The replay keeps every consumer (the ReAct loop, ACP session updates, the HTTP
// SSE bridge, the SPA transcript) on the single code path they already use for a
// live stream: one reasoning chunk, one text chunk, and one chunk per tool call.
// Text is deliberately not sliced into fake deltas - the answer is complete
// before the first chunk exists, and pretending otherwise would only add latency.
// Stop reason and token usage travel in the returned Response, as they do for
// every streaming provider; no chunk carries them.
type blockingProvider struct {
	inner Provider
}

func newBlockingProvider(inner Provider) Provider {
	if inner == nil {
		return nil
	}
	return &blockingProvider{inner: inner}
}

func (p *blockingProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return p.inner.Complete(ctx, messages, tools)
}

func (p *blockingProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	resp, err := p.inner.Complete(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	// A backend that ignores cancellation can answer after the user pressed Stop.
	// Replaying then would publish an answer and run its tool calls for a turn that
	// was already cancelled, so the finished response is dropped instead.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	if onChunk != nil {
		if resp.Reasoning != "" {
			onChunk(StreamChunk{ReasoningDelta: resp.Reasoning})
		}
		if resp.Content != "" {
			onChunk(StreamChunk{TextDelta: resp.Content})
		}
		for i := range resp.ToolCalls {
			tc := resp.ToolCalls[i]
			onChunk(StreamChunk{ToolCall: &tc})
		}
	}
	return resp, nil
}
