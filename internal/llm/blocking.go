package llm

import (
	"context"
	"fmt"
)

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
	if resp == nil {
		// Every provider returns a response or an error; a nil pair would reach the
		// ReAct loop as a nil dereference rather than a diagnosable failure.
		return nil, fmt.Errorf("llm: blocking completion returned no response")
	}

	// The replay is cancellable between chunks, and what comes back describes only
	// what the caller actually observed - the same contract a streaming provider has
	// when its stream is cut. Two callers depend on it. The ReAct loop guard cancels
	// mid-replay when a channel degenerates and then persists the returned response:
	// handing it the undelivered remainder would put an answer nobody saw, and tool
	// calls nobody ran, into the transcript, and replay those calls to the model with
	// no matching results. And a backend that ignores cancellation can answer after
	// the user pressed Stop, where nothing may be published at all.
	delivered := &Response{
		StopReason:         resp.StopReason,
		InputTokens:        resp.InputTokens,
		OutputTokens:       resp.OutputTokens,
		ReasoningSignature: resp.ReasoningSignature,
	}
	any := false
	for _, step := range replaySteps(resp) {
		if ctx.Err() != nil {
			break
		}
		if onChunk != nil {
			onChunk(step.chunk)
		}
		step.record(delivered)
		any = true
	}
	if err := ctx.Err(); err != nil {
		if !any {
			return nil, err
		}
		return delivered, err
	}
	return resp, nil
}

// replayStep is one chunk of the replay together with how it is recorded on the
// response the caller gets back, so emitting and accounting cannot drift apart.
type replayStep struct {
	chunk  StreamChunk
	record func(*Response)
}

// replaySteps orders a finished response into the chunk sequence a consumer sees:
// reasoning, then the answer text, then one chunk per tool call.
func replaySteps(resp *Response) []replayStep {
	steps := make([]replayStep, 0, 2+len(resp.ToolCalls))
	if resp.Reasoning != "" {
		steps = append(steps, replayStep{
			chunk:  StreamChunk{ReasoningDelta: resp.Reasoning},
			record: func(d *Response) { d.Reasoning = resp.Reasoning },
		})
	}
	if resp.Content != "" {
		steps = append(steps, replayStep{
			chunk:  StreamChunk{TextDelta: resp.Content},
			record: func(d *Response) { d.Content = resp.Content },
		})
	}
	for i := range resp.ToolCalls {
		tc := resp.ToolCalls[i]
		steps = append(steps, replayStep{
			chunk:  StreamChunk{ToolCall: &tc},
			record: func(d *Response) { d.ToolCalls = append(d.ToolCalls, tc) },
		})
	}
	return steps
}
