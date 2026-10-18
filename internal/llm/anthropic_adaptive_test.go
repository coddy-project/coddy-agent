package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tidwall/gjson"
)

func TestAnthropicAdaptiveRequestJSON(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, thinking, outputEffort string
		budget                                      int64
		// temperature reports whether the configured temperature (0.5) is sent.
		temperature bool
	}{
		{name: "opus 5.5 high", model: "claude-opus-5-5", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "opus 5.5 default keeps the API's effort", model: "claude-opus-5-5", thinking: "adaptive"},
		{name: "opus 5.5 low", model: "claude-opus-5-5", effort: "low", thinking: "adaptive", outputEffort: "low"},
		{name: "opus 5.5 xhigh", model: "claude-opus-5-5", effort: "xhigh", thinking: "adaptive", outputEffort: "xhigh"},
		{name: "opus 5.5 max", model: "claude-opus-5-5", effort: "max", thinking: "adaptive", outputEffort: "max"},
		{name: "opus 5.5 custom level unchanged", model: "claude-opus-5-5", effort: "custom", thinking: "adaptive", outputEffort: "custom"},
		{name: "opus 5.5 behind a gateway prefix", model: "anthropic/claude-opus-5-5", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "opus 5 default", model: "claude-opus-5", thinking: "adaptive"},
		{name: "opus 5 off", model: "claude-opus-5", effort: "off", thinking: "disabled"},
		{name: "sonnet 5.5 medium", model: "claude-sonnet-5-5", effort: "medium", thinking: "adaptive", outputEffort: "medium"},
		{name: "sonnet 5 high", model: "claude-sonnet-5", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "haiku 5.5 default", model: "claude-haiku-5-5", thinking: "adaptive"},
		{name: "fable 5.1 high", model: "claude-fable-5-1", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "opus 4.8 high", model: "claude-opus-4-8", effort: "high", thinking: "adaptive", outputEffort: "high"},
		{name: "opus 4.7 default stays without thinking", model: "claude-opus-4-7"},
		{name: "opus 4.7 off", model: "claude-opus-4-7", effort: "off", thinking: "disabled"},
		{name: "opus 4.6 keeps the budget", model: "claude-opus-4-6", effort: "high", thinking: "enabled", budget: 6144},
		{name: "sonnet 4.5 high keeps the budget", model: "claude-sonnet-4-5", effort: "high", thinking: "enabled", budget: 6144},
		{name: "sonnet 4.5 default", model: "claude-sonnet-4-5", temperature: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newAnthropicProvider(tc.model, "", "", nil, 8192, 0.5, tc.effort)
			raw, err := json.Marshal(p.buildParams("", nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(raw, "thinking.type").String(); got != tc.thinking {
				t.Errorf("thinking.type = %q, want %q", got, tc.thinking)
			}
			wantDisplay := ""
			if tc.thinking == "adaptive" {
				wantDisplay = "summarized"
			}
			if got := gjson.GetBytes(raw, "thinking.display").String(); got != wantDisplay {
				t.Errorf("thinking.display = %q, want %q", got, wantDisplay)
			}
			budget := gjson.GetBytes(raw, "thinking.budget_tokens")
			if budget.Int() != tc.budget || budget.Exists() != (tc.budget != 0) {
				t.Errorf("thinking.budget_tokens = %s, want %d (omitted when zero)", budget.Raw, tc.budget)
			}
			effort := gjson.GetBytes(raw, "output_config.effort")
			if effort.String() != tc.outputEffort || effort.Exists() != (tc.outputEffort != "") {
				t.Errorf("output_config.effort = %s, want %q (omitted when empty)", effort.Raw, tc.outputEffort)
			}
			if got := gjson.GetBytes(raw, "max_tokens").Int(); got != 8192 {
				t.Errorf("max_tokens = %d, want 8192", got)
			}
			if got := gjson.GetBytes(raw, "temperature").Exists(); got != tc.temperature {
				t.Errorf("temperature present = %v, want %v", got, tc.temperature)
			}
		})
	}
}

// TestAnthropicAdaptiveCallerTemperature pins that a temperature the caller
// asked for still travels to an adaptive model, for the backend to answer, as
// it does next to extended thinking.
func TestAnthropicAdaptiveCallerTemperature(t *testing.T) {
	p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0.2, "high")
	p.tempSet = true
	raw, err := json.Marshal(p.buildParams("", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(raw, "temperature"); !got.Exists() || got.Float() != 0.2 {
		t.Errorf("temperature = %s, want the caller's 0.2", got.Raw)
	}
}

func TestAnthropicAdaptiveRejectsOffBeforeRequest(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1"} {
		t.Run(model, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p := newAnthropicProvider(model, "test", srv.URL, srv.Client(), 8192, 0, "off")
			messages := []Message{{Role: RoleUser, Content: "hi"}}
			const want = "thinking cannot be turned off"
			if _, err := p.Complete(context.Background(), messages, nil); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Complete error = %v, want %q", err, want)
			}
			if _, err := p.Stream(context.Background(), messages, nil, func(StreamChunk) {}); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Stream error = %v, want %q", err, want)
			}
			if got := calls.Load(); got != 0 {
				t.Errorf("provider calls = %d, want zero", got)
			}
		})
	}
}

// replayedAssistant converts a history with one assistant turn and returns
// that turn as the request JSON sends it.
func replayedAssistant(t *testing.T, model, effort string, assistant Message) gjson.Result {
	t.Helper()
	p := newAnthropicProvider(model, "", "", nil, 8192, 0, effort)
	var tool []Message
	if len(assistant.ToolCalls) > 0 {
		tool = []Message{{Role: RoleTool, ToolCallID: assistant.ToolCalls[0].ID, Content: "result"}}
	}
	_, messages, err := p.splitMessages(append([]Message{{Role: RoleUser, Content: "read"}, assistant}, tool...))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p.buildParams("", messages, nil))
	if err != nil {
		t.Fatal(err)
	}
	return gjson.GetBytes(raw, "messages.1.content")
}

func TestAnthropicAdaptiveReplaysSignedReasoning(t *testing.T) {
	for _, effort := range []string{"high", ""} {
		t.Run("effort="+effort, func(t *testing.T) {
			content := replayedAssistant(t, "claude-opus-5-5", effort, Message{
				Role: RoleAssistant, Reasoning: "exact reasoning\n", ReasoningSignature: "signed",
				ToolCalls: []ToolCall{{ID: "call_1", Name: "read", InputJSON: "{}"}},
			})
			for path, want := range map[string]string{
				"0.type":      "thinking",
				"0.thinking":  "exact reasoning\n",
				"0.signature": "signed",
				"1.type":      "tool_use",
			} {
				if got := content.Get(path).String(); got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
		})
	}
}

// TestAnthropicThinkingCarrierReplay covers the answers a bare signature cannot
// describe: each thinking block comes back with its own signature, in the
// place the model wrote it.
func TestAnthropicThinkingCarrierReplay(t *testing.T) {
	call := ToolCall{ID: "call_1", Name: "read", InputJSON: `{"path":"a"}`}
	for _, tc := range []struct {
		name   string
		blocks []anthropicReplayBlock
		msg    Message
		want   []string
	}{
		{
			name: "empty thinking text of the omitted display",
			blocks: []anthropicReplayBlock{
				{Type: "thinking", Signature: "sigA"},
				{Type: "tool_use", ID: "call_1"},
			},
			msg:  Message{ToolCalls: []ToolCall{call}},
			want: []string{"thinking::sigA", "tool_use:call_1"},
		},
		{
			name: "a progress note before the tool call",
			blocks: []anthropicReplayBlock{
				{Type: "thinking", Thinking: "plan", Signature: "sigA"},
				{Type: "text", Text: "Let me read."},
				{Type: "thinking", Thinking: "progress", Signature: "sigB"},
				{Type: "tool_use", ID: "call_1"},
			},
			msg:  Message{Content: "Let me read.", Reasoning: "plan\n\nprogress", ToolCalls: []ToolCall{call}},
			want: []string{"thinking:plan:sigA", "text:Let me read.", "thinking:progress:sigB", "tool_use:call_1"},
		},
		{
			name: "a redacted block",
			blocks: []anthropicReplayBlock{
				{Type: "redacted_thinking", Data: "opaque"},
				{Type: "text", Text: "done"},
			},
			msg:  Message{Content: "done"},
			want: []string{"redacted_thinking:opaque", "text:done"},
		},
		{
			name: "a call cut off mid-input is left out",
			blocks: []anthropicReplayBlock{
				{Type: "thinking", Thinking: "plan", Signature: "sigA"},
				{Type: "tool_use", ID: "call_1"},
				{Type: "thinking", Thinking: "more", Signature: "sigB"},
				{Type: "tool_use", ID: "call_2"},
			},
			msg:  Message{ToolCalls: []ToolCall{call}},
			want: []string{"thinking:plan:sigA", "tool_use:call_1", "thinking:more:sigB"},
		},
		{
			name: "a block cut before its signature is left out",
			blocks: []anthropicReplayBlock{
				{Type: "thinking", Thinking: "plan", Signature: "sigA"},
				{Type: "text", Text: "Let me read."},
				{Type: "thinking", Thinking: "progress"},
			},
			msg:  Message{Content: "Let me read.", Reasoning: "plan\n\nprogress"},
			want: []string{"thinking:plan:sigA", "text:Let me read."},
		},
		{
			name: "text that changed since puts the thinking first",
			blocks: []anthropicReplayBlock{
				{Type: "text", Text: "original"},
				{Type: "thinking", Thinking: "plan", Signature: "sigA"},
				{Type: "tool_use", ID: "call_1"},
			},
			msg:  Message{Content: "edited", ToolCalls: []ToolCall{call}},
			want: []string{"thinking:plan:sigA", "text:edited", "tool_use:call_1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig := anthropicThinkingSignature(tc.blocks)
			if !strings.HasPrefix(sig, "{") {
				t.Fatalf("signature = %q, want a carrier", sig)
			}
			msg := tc.msg
			msg.Role, msg.ReasoningSignature = RoleAssistant, sig
			if got := replayedBlocks(replayedAssistant(t, "claude-opus-5-5", "high", msg)); strings.Join(got, " | ") != strings.Join(tc.want, " | ") {
				t.Errorf("replayed %v, want %v", got, tc.want)
			}
		})
	}
}

func replayedBlocks(content gjson.Result) []string {
	var out []string
	for _, b := range content.Array() {
		switch typ := b.Get("type").String(); typ {
		case "thinking":
			out = append(out, typ+":"+b.Get("thinking").String()+":"+b.Get("signature").String())
		case "redacted_thinking":
			out = append(out, typ+":"+b.Get("data").String())
		case "text":
			out = append(out, typ+":"+b.Get("text").String())
		case "tool_use":
			out = append(out, typ+":"+b.Get("id").String())
		}
	}
	return out
}

func TestAnthropicThinkingSignatureShapes(t *testing.T) {
	single := []anthropicReplayBlock{{Type: "thinking", Thinking: "plan", Signature: "sigA"}, {Type: "text", Text: "x"}}
	if got := anthropicThinkingSignature(single); got != "sigA" {
		t.Errorf("one signed block with text opening the answer = %q, want the bare signature", got)
	}
	if got := anthropicThinkingSignature([]anthropicReplayBlock{{Type: "thinking", Thinking: "cut"}, {Type: "text", Text: "x"}}); got != "" {
		t.Errorf("unsigned thinking = %q, want nothing to replay", got)
	}
	if got := anthropicThinkingSignature([]anthropicReplayBlock{{Type: "text", Text: "x"}}); got != "" {
		t.Errorf("no thinking = %q, want empty", got)
	}
}

// TestAnthropicReplaySkipsForeignCarrier pins that the reasoning another
// provider stored (a Codex or Devin carrier, after a model switch) never goes
// out as an Anthropic signature.
func TestAnthropicReplaySkipsForeignCarrier(t *testing.T) {
	for _, sig := range []string{`{"codex":true,"model":"gpt-5","items":[]}`, `{"devin":true,"model":"swe","signature":"x"}`} {
		content := replayedAssistant(t, "claude-opus-5-5", "high", Message{
			Role: RoleAssistant, Content: "done", Reasoning: "foreign", ReasoningSignature: sig,
		})
		if got := replayedBlocks(content); strings.Join(got, " | ") != "text:done" {
			t.Errorf("%s: replayed %v, want the text alone", sig, got)
		}
	}
}

// TestAnthropicParseResponseCarriesEveryThinkingBlock covers the non-streaming
// read of an answer with two signed thinking blocks.
func TestAnthropicParseResponseCarriesEveryThinkingBlock(t *testing.T) {
	var msg anthropic.Message
	if err := json.Unmarshal([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",
		"content":[{"type":"thinking","thinking":"plan","signature":"sigA"},{"type":"thinking","thinking":"","signature":"sigB"},
		{"type":"tool_use","id":"call_1","name":"read","input":{}}],"usage":{"input_tokens":1,"output_tokens":1}}`), &msg); err != nil {
		t.Fatal(err)
	}
	p := newAnthropicProvider("claude-opus-5-5", "", "", nil, 8192, 0, "high")
	resp, err := p.parseResponse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "plan" {
		t.Errorf("reasoning = %q, want plan", resp.Reasoning)
	}
	content := replayedAssistant(t, "claude-opus-5-5", "high", Message{
		Role: RoleAssistant, Reasoning: resp.Reasoning, ReasoningSignature: resp.ReasoningSignature, ToolCalls: resp.ToolCalls,
	})
	want := "thinking:plan:sigA | thinking::sigB | tool_use:call_1"
	if got := strings.Join(replayedBlocks(content), " | "); got != want {
		t.Errorf("replayed %s, want %s", got, want)
	}
}
