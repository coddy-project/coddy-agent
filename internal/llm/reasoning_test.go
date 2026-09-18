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
	params := p.buildParams(msgs, nil, true)
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
	npar := none.buildParams(msgs, nil, true)
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

// TestOpenAIBuildParamsQwenChatTemplateKwargs pins the Qwen3-family reasoning
// mapping: reasoning_effort plus chat_template_kwargs.enable_thinking, because
// Qwen thinking is a chat-template switch on OpenAI-compatible servers (vLLM,
// SGLang) and must be pinned on for the effort to take effect.
func TestOpenAIBuildParamsQwenChatTemplateKwargs(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	qwen := newOpenAIProvider("qwen3.6-35b-a3b", "", "", nil, 1024, 0.5, "medium")
	qpar := qwen.buildParams(msgs, nil, true)
	if qpar.ReasoningEffort != openai.ReasoningEffort("medium") {
		t.Errorf("reasoning_effort = %q, want medium", qpar.ReasoningEffort)
	}
	if !qpar.MaxCompletionTokens.Valid() {
		t.Error("expected max_completion_tokens for reasoning model")
	}
	qb, err := json.Marshal(qpar)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(qb), `"chat_template_kwargs":{"enable_thinking":true}`) {
		t.Errorf("qwen reasoning request must pin enable_thinking on: %s", qb)
	}

	// gpt-oss maps to reasoning_effort only; chat_template_kwargs is a Qwen
	// chat-template switch and must not leak to other model families.
	oss := newOpenAIProvider("gpt-oss-120b", "", "", nil, 1024, 0.5, "high")
	obar, err := json.Marshal(oss.buildParams(msgs, nil, true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(obar), `"reasoning_effort":"high"`) {
		t.Errorf("gpt-oss reasoning_effort missing: %s", obar)
	}
	if strings.Contains(string(obar), "chat_template_kwargs") {
		t.Errorf("chat_template_kwargs must not be set for non-qwen models: %s", obar)
	}

	// Qwen without a reasoning level keeps the plain chat path (no effort, no kwargs).
	plain := newOpenAIProvider("qwen3.6-35b-a3b", "", "", nil, 1024, 0.5, "")
	ppar := plain.buildParams(msgs, nil, true)
	if ppar.ReasoningEffort != "" {
		t.Errorf("reasoning_effort = %q, want empty", ppar.ReasoningEffort)
	}
	if !ppar.MaxTokens.Valid() {
		t.Error("expected max_tokens for no-level qwen request")
	}
	pb, err := json.Marshal(ppar)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pb), "chat_template_kwargs") {
		t.Errorf("no-level qwen request must stay plain: %s", pb)
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

// TestBuildParamsSendsARequestedZeroTemperature pins the difference between a
// configured temperature, where zero means "not configured", and one a caller
// asked for, where zero is a value like any other.
func TestBuildParamsSendsARequestedZeroTemperature(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	configured := newOpenAIProvider("gpt-4o", "", "", nil, 1024, 0, "")
	if configured.buildParams(msgs, nil, true).Temperature.Valid() {
		t.Error("openai: an unconfigured temperature must stay off the request")
	}
	requested := newOpenAIProvider("gpt-4o", "", "", nil, 1024, 0, "")
	requested.tempSet = true
	if got := requested.buildParams(msgs, nil, true).Temperature; !got.Valid() || got.Value != 0 {
		t.Errorf("openai: requested temperature 0 = %+v, want an explicit 0", got)
	}

	anthConfigured := newAnthropicProvider("claude-3-5-haiku", "", "", nil, 1024, 0, "")
	if anthConfigured.buildParams("", nil, nil).Temperature.Valid() {
		t.Error("anthropic: an unconfigured temperature must stay off the request")
	}
	anthRequested := newAnthropicProvider("claude-3-5-haiku", "", "", nil, 1024, 0, "")
	anthRequested.tempSet = true
	if got := anthRequested.buildParams("", nil, nil).Temperature; !got.Valid() || got.Value != 0 {
		t.Errorf("anthropic: requested temperature 0 = %+v, want an explicit 0", got)
	}
}

func TestRequestOptionsValidateAndApply(t *testing.T) {
	intp := func(v int) *int { return &v }
	floatp := func(v float64) *float64 { return &v }
	for name, tc := range map[string]struct {
		providerType string
		opts         RequestOptions
		wantErr      string
	}{
		"nothing asked":               {"codex", RequestOptions{}, ""},
		"openai cap and temperature":  {"openai", RequestOptions{MaxTokens: intp(1), Temperature: floatp(2)}, ""},
		"neuraldeep temperature 0":    {"neuraldeep", RequestOptions{Temperature: floatp(0)}, ""},
		"anthropic temperature 1":     {"anthropic", RequestOptions{Temperature: floatp(1)}, ""},
		"zero cap":                    {"openai", RequestOptions{MaxTokens: intp(0)}, "max_tokens must be a positive integer"},
		"openai temperature too high": {"openai", RequestOptions{Temperature: floatp(2.01)}, "between 0 and 2"},
		"anthropic temperature 1.01":  {"anthropic", RequestOptions{Temperature: floatp(1.01)}, "between 0 and 1"},
		"codex cap":                   {"codex", RequestOptions{MaxTokens: intp(64)}, "max_tokens is not supported by a codex model"},
		"codex temperature":           {"codex", RequestOptions{Temperature: floatp(0)}, "temperature is not supported by a codex model"},
	} {
		err := tc.opts.Validate(tc.providerType)
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: Validate = %v, want %q", name, err, tc.wantErr)
		}
	}

	in := ProviderInput{MaxTokens: 8192, Temperature: 0.2}
	RequestOptions{}.Apply(&in)
	if in.MaxTokens != 8192 || in.Temperature != 0.2 || in.TemperatureSet {
		t.Fatalf("empty options changed the input: %+v", in)
	}
	RequestOptions{MaxTokens: intp(256), Temperature: floatp(0)}.Apply(&in)
	if in.MaxTokens != 256 || in.Temperature != 0 || !in.TemperatureSet {
		t.Fatalf("options not applied: %+v", in)
	}
}

// TestBuildParamsSendsARequestedTemperatureNextToReasoning pins that a
// configured temperature stays off a reasoning request while one the caller
// asked for is sent, leaving the verdict to the backend.
func TestBuildParamsSendsARequestedTemperatureNextToReasoning(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	oai := newOpenAIProvider("qwen3.6-35b-a3b", "", "", nil, 1024, 0.6, "high")
	oai.tempSet = true
	if got := oai.buildParams(msgs, nil, true).Temperature; !got.Valid() || got.Value != 0.6 {
		t.Errorf("openai: requested temperature next to reasoning = %+v, want 0.6", got)
	}

	anth := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 1, "high")
	anth.tempSet = true
	params := anth.buildParams("", nil, nil)
	if params.Thinking.OfEnabled == nil {
		t.Fatal("anthropic: thinking must stay enabled")
	}
	if got := params.Temperature; !got.Valid() || got.Value != 1 {
		t.Errorf("anthropic: requested temperature next to thinking = %+v, want 1", got)
	}
}

func TestRequestOptionsCapAgainstAnthropicThinking(t *testing.T) {
	intp := func(v int) *int { return &v }
	for name, tc := range map[string]struct {
		providerType, level string
		maxTokens           int
		wantErr             string
	}{
		"room above the budget":    {"anthropic", "high", 4096, ""},
		"no reasoning, tiny cap":   {"anthropic", "", 16, ""},
		"cap equal to the minimum": {"anthropic", "low", 1024, "max_tokens must exceed 1024"},
		"cap below the minimum":    {"anthropic", "high", 1000, "max_tokens must exceed 1024"},
		"openai has no budget":     {"openai", "high", 16, ""},
	} {
		err := RequestOptions{MaxTokens: intp(tc.maxTokens), ReasoningEffort: tc.level}.Validate(tc.providerType)
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: Validate = %v, want %q", name, err, tc.wantErr)
		}
	}

	in := ProviderInput{ReasoningEffort: ""}
	RequestOptions{ReasoningEffort: "none"}.Apply(&in)
	if in.ReasoningEffort != "none" {
		t.Fatalf("reasoning level not applied: %+v", in)
	}
}

// TestBuildParamsReasoningOff pins what the "off" pseudo level becomes on each
// wire: Qwen3 turns the chat-template switch off and sends no effort, another
// OpenAI-compatible model is asked for "none", Anthropic sends no thinking
// block, and the Codex backend is asked for "none".
func TestBuildParamsReasoningOff(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}}

	qwen := newOpenAIProvider("qwen3.6-35b-a3b", "", "", nil, 1024, 0.5, "off")
	qpar := qwen.buildParams(msgs, nil, true)
	if qpar.ReasoningEffort != "" {
		t.Errorf("qwen off: reasoning_effort = %q, want empty", qpar.ReasoningEffort)
	}
	if !qpar.MaxTokens.Valid() {
		t.Error("qwen off: expected max_tokens, the request is a plain chat request")
	}
	qb, err := json.Marshal(qpar)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(qb), `"chat_template_kwargs":{"enable_thinking":false}`) {
		t.Errorf("qwen off must turn thinking off in the template: %s", qb)
	}

	gpt := newOpenAIProvider("gpt-5.1", "", "", nil, 1024, 0.5, "off")
	gb, err := json.Marshal(gpt.buildParams(msgs, nil, true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gb), `"reasoning_effort":"none"`) {
		t.Errorf("off on an OpenAI reasoning model asks for none: %s", gb)
	}
	if strings.Contains(string(gb), "chat_template_kwargs") {
		t.Errorf("chat_template_kwargs must not leak to non-qwen models: %s", gb)
	}

	claude := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "off")
	if params := claude.buildParams("", nil, nil); params.Thinking.OfEnabled != nil {
		t.Error("anthropic off must not enable thinking")
	}

	if got := codexReasoningEffort("off"); got != "none" {
		t.Errorf("codex off = %q, want none", got)
	}
}
