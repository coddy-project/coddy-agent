//go:build http

package httpserver

// The real handler behind httptest and the real `coddy` provider of internal/llm
// in front of it, with a stub provider behind the factory: the two ends of the
// wire built here and in internal/llm agree.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (fx *sharedFixture) clientInput(token string) llm.ProviderInput {
	return llm.ProviderInput{
		Name:     "remote",
		Type:     "coddy",
		Model:    sharedTestAlias,
		APIKey:   token,
		BaseURL:  fx.ts.URL,
		ProxyURL: "none",
	}
}

func (fx *sharedFixture) client(t *testing.T, mut ...func(*llm.ProviderInput)) llm.Provider {
	t.Helper()
	in := fx.clientInput(sharedTestSharedTok)
	for _, m := range mut {
		m(&in)
	}
	p, err := llm.NewProvider(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSharedRoundTripWithTheCoddyProvider(t *testing.T) {
	fx := newSharedFixture(t)
	fx.stub.run = func(_ context.Context, call int, msgs []llm.Message, tools []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		if call == 1 {
			call := llm.ToolCall{ID: "call_weather_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}
			on(llm.StreamChunk{ReasoningDelta: "Check the file first."})
			on(llm.StreamChunk{ToolCall: &call})
			return &llm.Response{Reasoning: "Check the file first.", ReasoningSignature: "sig-abc", ToolCalls: []llm.ToolCall{call},
				InputTokens: 120, OutputTokens: 8, CachedInputTokens: 100, StopReason: "tool_use"}, nil
		}
		on(llm.StreamChunk{TextDelta: "It is 18C in Paris."})
		return &llm.Response{Content: "It is 18C in Paris.", InputTokens: 140, OutputTokens: 6}, nil
	}
	p := fx.client(t)
	tools := []llm.ToolDefinition{{Name: "get_weather", Description: "Weather", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}}

	var chunks []llm.StreamChunk
	resp, err := p.Stream(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "You are the local harness."},
		{Role: llm.RoleUser, Content: "Weather in Paris?"},
	}, tools, func(c llm.StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_weather_1" || resp.ToolCalls[0].InputJSON != `{"city":"Paris"}` {
		t.Fatalf("tool calls: %+v", resp.ToolCalls)
	}
	if resp.InputTokens != 120 || resp.OutputTokens != 8 || resp.CachedInputTokens != 100 || len(chunks) != 2 {
		t.Fatalf("usage/chunks: %+v %d", resp, len(chunks))
	}
	if resp.ReasoningSignature == "" || resp.ReasoningSignature == "sig-abc" {
		t.Fatalf("the signature came back unsealed: %q", resp.ReasoningSignature)
	}
	got := fx.stub.lastMessages()
	if len(got) != 2 || got[0].Content != "You are the local harness." {
		t.Fatalf("the provider saw %+v", got)
	}

	// The next turn: the history carries the assistant message with its sealed
	// signature and the tool result. The remote's provider sees the raw one.
	resp2, err := p.Stream(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "Weather in Paris?"},
		{Role: llm.RoleAssistant, Reasoning: resp.Reasoning, ReasoningSignature: resp.ReasoningSignature, ToolCalls: resp.ToolCalls},
		{Role: llm.RoleTool, ToolCallID: "call_weather_1", Content: "18C"},
	}, tools, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Content != "It is 18C in Paris." {
		t.Fatalf("answer: %+v", resp2)
	}
	seen := fx.stub.lastMessages()
	if len(seen) != 3 || seen[1].ReasoningSignature != "sig-abc" || seen[2].ToolCallID != "call_weather_1" || seen[2].Content != "18C" {
		t.Fatalf("the provider saw %+v", seen)
	}
}

func TestSharedListingThroughTheCoddyProvider(t *testing.T) {
	fx := newSharedFixture(t)
	entries, err := llm.ListModels(context.Background(), fx.clientInput(sharedTestSharedTok))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != sharedTestAlias || entries[0].ContextWindow != 200000 || entries[0].Revision == "" ||
		strings.Join(entries[0].ReasoningLevels, ",") != "low,high" || entries[0].ReasoningDefault != "low" {
		t.Fatalf("entries: %+v", entries)
	}
}

// A request made on a stale view is refused before the provider is called, the
// client refreshes its view and sends it once more, and the provider runs once.
func TestSharedStaleViewIsRefreshedAndRetriedOnce(t *testing.T) {
	fx := newSharedFixture(t)
	fx.stub.run = func(_ context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		on(llm.StreamChunk{TextDelta: "Done after the refresh."})
		return &llm.Response{Content: "Done after the refresh."}, nil
	}
	var refreshes int
	p := fx.client(t, func(in *llm.ProviderInput) {
		in.ExpectedRevision = "the-revision-of-an-older-listing"
		in.RefreshCapabilities = func(ctx context.Context) (*llm.ModelEntry, error) {
			refreshes++
			entries, err := llm.ListModels(ctx, fx.clientInput(sharedTestSharedTok))
			if err != nil || len(entries) == 0 {
				return nil, err
			}
			return &entries[0], nil
		}
	})
	resp, err := p.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Done after the refresh." || refreshes != 1 || fx.stub.callCount() != 1 {
		t.Fatalf("answer %q refreshes %d provider calls %d", resp.Content, refreshes, fx.stub.callCount())
	}
}

func TestSharedRefusalsReachTheCoddyProviderTyped(t *testing.T) {
	fx := newSharedFixture(t)
	msg := []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}

	_, err := fx.client(t, func(in *llm.ProviderInput) { in.APIKey = "a-wrong-token" }).Complete(context.Background(), msg, nil)
	if llm.CoddyErrorKind(err) != llm.WireKindAuth {
		t.Fatalf("a wrong token: kind %q (%v)", llm.CoddyErrorKind(err), err)
	}
	_, err = fx.client(t, func(in *llm.ProviderInput) { in.Model = sharedTestSelector }).Complete(context.Background(), msg, nil)
	if llm.CoddyErrorKind(err) != llm.WireKindInvalid || strings.Contains(err.Error(), "qwen3") {
		t.Fatalf("the selector as the model: kind %q (%v)", llm.CoddyErrorKind(err), err)
	}
	fx.stub.run = func(context.Context, int, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
		return nil, errors.New("boom")
	}
	_, err = fx.client(t).Complete(context.Background(), msg, nil)
	if llm.CoddyErrorKind(err) != llm.WireKindUpstream || strings.Contains(err.Error(), "boom") {
		t.Fatalf("a provider failure: kind %q (%v)", llm.CoddyErrorKind(err), err)
	}
}

// With every slot taken the client is told busy, waits within its budget and is
// served once a slot frees; the provider runs for it exactly once.
func TestSharedBusyRemoteIsWaitedOutByTheCoddyProvider(t *testing.T) {
	fx := newSharedFixture(t, withSharedConfig(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = 1 }))
	hold := make(chan struct{})
	entered := make(chan struct{}, 4)
	var mu sync.Mutex
	calls := 0
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		entered <- struct{}{}
		if first {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		on(llm.StreamChunk{TextDelta: "Done after the wait."})
		return &llm.Response{Content: "Done after the wait."}, nil
	}
	holder := newSSEReader(t, fx.complete(wireReq(sharedTestAlias)))
	<-entered

	type result struct {
		resp *llm.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		p := fx.client(t, func(in *llm.ProviderInput) { in.BusyWait = 10 * time.Second })
		resp, err := p.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}, nil)
		done <- result{resp, err}
	}()
	// The client is told busy and sleeps; the held stream ends meanwhile.
	time.Sleep(300 * time.Millisecond)
	close(hold)
	holder.all()
	select {
	case r := <-done:
		if r.err != nil || r.resp.Content != "Done after the wait." {
			t.Fatalf("the waiting client: %+v %v", r.resp, r.err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting client was never served")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("the provider ran %d times, want 2 (one held, one for the waiting client)", calls)
	}
}
