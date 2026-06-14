package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

func TestOpenAIBuildParamsReasoningEffort(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	p := newOpenAIProvider("gpt-5", "", "", nil, 1024, 0.5, "high")
	params := p.buildParams(msgs, nil)
	if params.ReasoningEffort != openai.ReasoningEffort("high") {
		t.Errorf("reasoning_effort = %q, want high", params.ReasoningEffort)
	}
	// Reasoning models reject max_tokens and custom temperature.
	if !params.MaxCompletionTokens.Valid() {
		t.Error("expected max_completion_tokens for reasoning model")
	}
	if params.MaxTokens.Valid() {
		t.Error("max_tokens must not be set for reasoning model")
	}
	if params.Temperature.Valid() {
		t.Error("temperature must not be set for reasoning model")
	}

	// Empty reasoning effort is omitted; non-reasoning models keep max_tokens + temperature.
	none := newOpenAIProvider("gpt-4o", "", "", nil, 1024, 0.5, "")
	npar := none.buildParams(msgs, nil)
	if npar.ReasoningEffort != "" {
		t.Errorf("reasoning_effort = %q, want empty", npar.ReasoningEffort)
	}
	if !npar.MaxTokens.Valid() {
		t.Error("expected max_tokens for non-reasoning model")
	}
	if npar.MaxCompletionTokens.Valid() {
		t.Error("max_completion_tokens must not be set for non-reasoning model")
	}
	if !npar.Temperature.Valid() {
		t.Error("expected temperature for non-reasoning model")
	}
}

func TestAnthropicBuildParamsThinking(t *testing.T) {
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "high")
	params := p.buildParams("", nil, nil)

	if params.Thinking.OfEnabled == nil {
		t.Fatal("expected thinking enabled for reasoning level high")
	}
	if got := params.Thinking.OfEnabled.BudgetTokens; got <= 0 || got >= 8192 {
		t.Errorf("budget_tokens = %d, want >0 and < max_tokens(8192)", got)
	}
	// Extended thinking requires temperature to be unset.
	if params.Temperature.Valid() {
		t.Error("temperature must be unset when thinking is enabled")
	}
}

func TestAnthropicBuildParamsNoThinkingKeepsTemperature(t *testing.T) {
	p := newAnthropicProvider("claude-3-5-sonnet", "", "", nil, 8192, 0.7, "")
	params := p.buildParams("", nil, nil)
	if params.Thinking.OfEnabled != nil {
		t.Error("thinking must be disabled when no reasoning level set")
	}
	if !params.Temperature.Valid() {
		t.Error("temperature should be set when thinking is disabled")
	}
}

func TestAnthropicReplaysSignedThinkingBlockBeforeToolUse(t *testing.T) {
	// Extended thinking + tool use requires the signed thinking block to be replayed
	// first, with the exact reasoning text, or the Anthropic API rejects the turn.
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "high")
	msgs := []Message{
		{
			Role:               RoleAssistant,
			Content:            "calling tool",
			Reasoning:          "step by step",
			ReasoningSignature: "sig-abc",
			ToolCalls:          []ToolCall{{ID: "t1", Name: "read", InputJSON: "{}"}},
		},
	}
	_, conv := p.splitMessages(msgs)
	b, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	thinkIdx := strings.Index(s, `"type":"thinking"`)
	toolIdx := strings.Index(s, `"type":"tool_use"`)
	if thinkIdx < 0 {
		t.Fatalf("expected a thinking block in replayed assistant message: %s", s)
	}
	if !strings.Contains(s, `"signature":"sig-abc"`) {
		t.Errorf("expected signature replayed verbatim: %s", s)
	}
	if !strings.Contains(s, `"thinking":"step by step"`) {
		t.Errorf("expected exact reasoning text replayed: %s", s)
	}
	if toolIdx < 0 || thinkIdx > toolIdx {
		t.Errorf("thinking block must precede tool_use (think=%d tool=%d)", thinkIdx, toolIdx)
	}
}

func TestAnthropicOmitsThinkingBlockWhenDisabled(t *testing.T) {
	// With no reasoning level, the stored signature must NOT be replayed (thinking off).
	p := newAnthropicProvider("claude-3-5-sonnet", "", "", nil, 8192, 0.7, "")
	msgs := []Message{
		{Role: RoleAssistant, Content: "hi", Reasoning: "x", ReasoningSignature: "sig", ToolCalls: []ToolCall{{ID: "t1", Name: "read", InputJSON: "{}"}}},
	}
	_, conv := p.splitMessages(msgs)
	b, _ := json.Marshal(conv)
	if strings.Contains(string(b), `"type":"thinking"`) {
		t.Errorf("thinking block must be omitted when thinking disabled: %s", string(b))
	}
}

func TestAnthropicParseResponseCapturesThinking(t *testing.T) {
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0, "high")
	var resp anthropic.Message
	raw := `{"content":[{"type":"thinking","thinking":"because","signature":"sig-xyz"},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	r, err := p.parseResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if r.Reasoning != "because" || r.ReasoningSignature != "sig-xyz" {
		t.Errorf("captured reasoning=%q sig=%q, want because/sig-xyz", r.Reasoning, r.ReasoningSignature)
	}
	if r.Content != "answer" {
		t.Errorf("content=%q, want answer", r.Content)
	}
}

func TestAnthropicThinkingBudgetBumpsMaxTokens(t *testing.T) {
	// Tiny max_tokens still yields a valid budget < max_tokens after bump.
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 512, 0, "high")
	params := p.buildParams("", nil, nil)
	if params.Thinking.OfEnabled == nil {
		t.Fatal("expected thinking enabled")
	}
	if params.Thinking.OfEnabled.BudgetTokens >= params.MaxTokens {
		t.Errorf("budget %d must be < max_tokens %d", params.Thinking.OfEnabled.BudgetTokens, params.MaxTokens)
	}
}
