package agent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// What a model's reasoning cost travels with the step that spent it: on the
// token_usage update a client reads live and on the assistant message a reload
// reads (issue #258).

type usageCaptureSender struct {
	mu     sync.Mutex
	usages []acp.TokenUsageUpdate
}

func (s *usageCaptureSender) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.TokenUsageUpdate); ok {
		s.mu.Lock()
		s.usages = append(s.usages, u)
		s.mu.Unlock()
	}
	return nil
}

func (s *usageCaptureSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (s *usageCaptureSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func reasoningStep(reasoning, answer string, reasoningTokens int) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		if reasoning != "" {
			onChunk(llm.StreamChunk{ReasoningDelta: reasoning})
		}
		onChunk(llm.StreamChunk{TextDelta: answer})
		return &llm.Response{Content: answer, StopReason: "end_turn", InputTokens: 10, OutputTokens: 30, ReasoningTokens: reasoningTokens}
	}
}

func lastAssistant(t *testing.T, st *session.State) llm.Message {
	t.Helper()
	msgs := st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i]
		}
	}
	t.Fatalf("no assistant message in %+v", msgs)
	return llm.Message{}
}

func TestTurnKeepsWhatTheReasoningCost(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reasoning string
	}{
		{"reasoning shown", "Weighing it."},
		// A provider that keeps the text to itself (OpenAI's o-series behind an
		// aggregator) still says what the reasoning cost.
		{"reasoning hidden", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &session.State{ID: "sess_reasoning_tokens", CWD: t.TempDir(), Mode: session.ModeAgent}
			sender := &usageCaptureSender{}
			ag := NewAgent(wakeTurnConfig(), st, sender, nil)
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
				return scripted(reasoningStep(tc.reasoning, "Done.", 25)), nil
			}
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "go"}}); err != nil {
				t.Fatal(err)
			}

			msg := lastAssistant(t, st)
			if msg.Reasoning != tc.reasoning || msg.ReasoningTokens != 25 {
				t.Fatalf("assistant message reasoning = %q / %d tokens, want %q / 25", msg.Reasoning, msg.ReasoningTokens, tc.reasoning)
			}
			sender.mu.Lock()
			defer sender.mu.Unlock()
			if len(sender.usages) != 1 || sender.usages[0].ReasoningTokens != 25 || sender.usages[0].OutputTokens != 30 {
				t.Fatalf("token_usage updates = %+v, want one with 30 output and 25 reasoning tokens", sender.usages)
			}
		})
	}
}

// A provider that reports nothing about reasoning leaves the message and the
// update without a count, which is not the same as a count of zero.
func TestTurnWithoutAReasoningCountStoresNone(t *testing.T) {
	st := &session.State{ID: "sess_reasoning_none", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &usageCaptureSender{}
	ag := NewAgent(wakeTurnConfig(), st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return scripted(reasoningStep("Weighing it.", "Done.", 0)), nil
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	if msg := lastAssistant(t, st); msg.ReasoningTokens != 0 {
		t.Fatalf("reasoning tokens = %d, want none", msg.ReasoningTokens)
	}
}

// paramsState is a session that knows what its provider's listing said the
// model takes, as a manager-built session does once the listing was read.
type paramsState struct {
	*session.State
	params map[string][]string
	asked  []string
}

func (s *paramsState) ModelParameters(_ *config.Config, providerName, apiModel string) []string {
	s.asked = append(s.asked, providerName+"/"+apiModel)
	return s.params[providerName+"/"+apiModel]
}

func TestProviderInputCarriesTheListedParameters(t *testing.T) {
	cfg := wakeTurnConfig()
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "agg", Type: "openai", APIBase: "https://agg.example/v1", APIKey: "k"})
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "agg/qwen/qwen3.8-flash", MaxContextTokens: 128000})
	cfg.Agent.Model = "agg/qwen/qwen3.8-flash"
	listed := []string{"max_tokens", "reasoning", "tools"}
	st := &paramsState{
		State:  &session.State{ID: "sess_listed_params", CWD: t.TempDir(), Mode: session.ModeAgent},
		params: map[string][]string{"agg/qwen/qwen3.8-flash": listed},
	}
	var inputs []llm.ProviderInput
	ag := NewAgent(cfg, st, &usageCaptureSender{}, nil)
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		inputs = append(inputs, in)
		return scripted(answerStep("ok")), nil
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no provider was built")
	}
	if got := inputs[0].SupportedParameters; !slices.Equal(got, listed) {
		t.Fatalf("provider input parameters = %v, want %v (asked for %v)", got, listed, st.asked)
	}
}

// awaitingParamsState knows a model's listed parameters only once the turn
// waited for that model's provider listing, as a manager-built session does
// when the listing has not been read yet.
type awaitingParamsState struct {
	*session.State
	params  map[string][]string
	awaited map[string]bool
}

func (s *awaitingParamsState) AwaitContextWindow(_ context.Context, _ *config.Config, modelRef string) {
	s.awaited[modelRef] = true
}

func (s *awaitingParamsState) ModelParameters(_ *config.Config, providerName, apiModel string) []string {
	ref := providerName + "/" + apiModel
	if !s.awaited[ref] {
		return nil
	}
	return s.params[ref]
}

// A switch to another model in the middle of a turn reads that model's listing
// before its transport is built, so the very next request already sends the
// reasoning level the way the listing names it.
func TestMidTurnSwitchReadsTheListingBeforeBuildingTheProvider(t *testing.T) {
	base, st, client := modelToolsAgent(t, session.ModeAgent, "fake/a",
		toolRow{Model: "fake/a"}, toolRow{Model: "fake/qwen/qwen3.8-flash"})
	listed := []string{"max_tokens", "reasoning", "tools"}
	ws := &awaitingParamsState{
		State:   st,
		params:  map[string][]string{"fake/qwen/qwen3.8-flash": listed},
		awaited: map[string]bool{},
	}
	ag := NewAgent(base.cfg, ws, client, nil)
	provider := &switchingProvider{st: st, to: "fake/qwen/qwen3.8-flash"}
	provider.toolCall = &llm.ToolCall{ID: "call_read", Name: "read", InputJSON: `{"path":"missing.txt"}`}
	var inputs []llm.ProviderInput
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		inputs = append(inputs, in)
		return provider, nil
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "look around"}}); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[1].Model != "qwen/qwen3.8-flash" {
		t.Fatalf("providers built for %+v, want the switch to qwen/qwen3.8-flash", inputs)
	}
	if got := inputs[1].SupportedParameters; !slices.Equal(got, listed) {
		t.Fatalf("the provider after the switch was built with %v, want %v", got, listed)
	}
}
