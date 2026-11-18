package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

// An attempt that never reached an answer is not a call of the account: the
// retry that follows it is the same request, and journaling the failure once
// per attempt inflated both the call count and the unknown-usage count.
func TestResilientJournalsOnlyTheAttemptsThatAnswered(t *testing.T) {
	store := statistics.New(t.TempDir())
	ctx, recorder := statistics.Begin(context.Background(), store, statistics.Event{Session: "chat", Project: "/w"}, func(err error) { t.Fatal(err) })
	attempts := 0
	inner := &stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("connection reset by peer")
		}
		return &Response{Content: "done", InputTokens: 100, OutputTokens: 20}, nil
	}}
	p := WrapResilient(inner, ResilientOptions{Name: "openai", Model: "gpt-5", RetryMax: 3, RetryBase: time.Millisecond, RetryMaxDelay: 2 * time.Millisecond})
	if _, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	recorder.Finish()
	if attempts != 3 {
		t.Fatalf("the stub answered on attempt %d", attempts)
	}
	report, err := store.Query(statistics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Calls != 1 {
		t.Fatalf("two failed attempts were journaled as calls: %+v", report.Totals)
	}
	if report.Totals.UnknownUsage != 0 {
		t.Fatalf("a failed attempt was journaled as usage nobody knows: %+v", report.Totals)
	}
	if report.Totals.Total == nil || *report.Totals.Total != 120 {
		t.Fatalf("the answered attempt was not counted: %+v", report.Totals)
	}
}
