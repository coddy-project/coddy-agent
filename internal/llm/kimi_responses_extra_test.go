package llm

import (
	"context"
	"testing"
)

func TestKimiResponsesReviewDistinctOutputIndices(t *testing.T) {
	p := kimiResponsesFixture(t, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"a","name":"one"}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"b","name":"two"}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","arguments":"{}"}}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","arguments":"{}"}}

data: {"type":"response.completed"}

`)
	r, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if err != nil || r == nil || len(r.ToolCalls) != 2 || r.ToolCalls[0].ID != "a" || r.ToolCalls[1].ID != "b" {
		t.Fatalf("response=%#v err=%v", r, err)
	}
}
func TestKimiResponsesReviewCutJSON(t *testing.T) {
	p := kimiResponsesFixture(t, "data: {\"type\":\n\n")
	_, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) || !isRetryableLLMError(err) {
		t.Fatalf("err=%v", err)
	}
}
