//go:build http

package httpserver

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestValidateChatParameters(t *testing.T) {
	cases := []struct {
		name, body, param string
		profile           bool
		topP              *float64
	}{
		{name: "ordinary", body: `{"model":"test","messages":[],"stream":true}`},
		{name: "message name", body: `{"messages":[{"role":"user","content":"hi","name":"alice"}]}`, param: "messages[0].name"},
		{name: "unknown message", body: `{"messages":[{"role":"user","content":"hi","other":1}]}`, param: "messages[0].other"},
		{name: "unknown call", body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"},"other":1}]}]}`, param: "messages[0].tool_calls[0].other"},
		{name: "unknown function", body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}","extra":1}}]}]}`, param: "messages[0].tool_calls[0].function.extra"},
		{name: "image detail", body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA==","detail":"high"}}]}]}`, param: "messages[0].content[0].image_url.detail"},
		{name: "image url unknown", body: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x","extra":true}}]}]}`, param: "messages[0].content[0].image_url.extra"},
		{name: "content unknown", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"hi","extra":1}]}]}`, param: "messages[0].content[0].extra"},
		{name: "valid history", body: `{"messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"ok"}]}`},
		{name: "direct metadata reasoning", body: `{"metadata":{"reasoning":"high","session_id":"s"}}`, param: "metadata.reasoning"},
		{name: "profile metadata reasoning", body: `{"metadata":{"reasoning":"high"}}`, profile: true},
		{name: "direct metadata unrelated", body: `{"metadata":{"session_id":"s","custom":"v"}}`},
		{name: "opaque metadata string", body: `{"metadata":"{\"reasoning\":\"high\"}"}`},
		{name: "non-stream usage", body: `{"stream":false,"stream_options":{"include_usage":true}}`, param: "stream_options"},
		{name: "stream usage", body: `{"stream":true,"stream_options":{"include_usage":true}}`},
		{name: "top_p", body: `{"top_p":0.7}`, topP: floatPtr(0.7)},
		{name: "zero", body: `{"top_p":0}`, topP: floatPtr(0)},
		{name: "null", body: `{"top_p":null}`},
		{name: "bad top_p", body: `{"top_p":1.1}`, param: "top_p"},
		{name: "top_p type", body: `{"top_p":"0.7"}`, param: "top_p"},
		{name: "profile top_p", body: `{"top_p":0.5}`, profile: true, param: "top_p"},
		{name: "unknown", body: `{"future":true}`, param: "future"},
		{name: "n", body: `{"n":2}`, param: "n"},
		{name: "response format", body: `{"response_format":{"type":"json_object"}}`, param: "response_format"},
		{name: "auto", body: `{"tool_choice":"auto"}`},
		{name: "none", body: `{"tool_choice":"none"}`},
		{name: "required", body: `{"tool_choice":"required"}`, param: "tool_choice"},
		{name: "named function", body: `{"tool_choice":{"type":"function","function":{"name":"f"}}}`, param: "tool_choice"},
		{name: "nested unknown", body: `{"tool_choice":{"type":"function","function":{"name":"f","unexpected":true}}}`, param: "tool_choice"},
		{name: "tool strict", body: `{"tools":[{"type":"function","function":{"name":"f","strict":true}}]}`, param: "tools"},
		{name: "tool unknown", body: `{"tools":[{"type":"function","extra":true,"function":{"name":"f"}}]}`, param: "tools"},
		{name: "tool schema", body: `{"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","additionalProperties":false}}}]}`},
		{name: "profile tools", body: `{"tools":[]}`, profile: true, param: "tools"},
		{name: "profile choice", body: `{"tool_choice":"none"}`, profile: true, param: "tool_choice"},
		{name: "profile max tokens", body: `{"max_tokens":3}`, profile: true, param: "max_tokens"},
		{name: "stream options unknown", body: `{"stream_options":{"include_usage":true,"future":true}}`, param: "stream_options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := validateChatParameters(json.RawMessage(tc.body), tc.profile)
			if tc.param != "" {
				if err == nil || err.Param != tc.param {
					t.Fatalf("got %v, want error param %s", err, tc.param)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if (opts.TopP == nil) != (tc.topP == nil) || opts.TopP != nil && *opts.TopP != *tc.topP {
				t.Fatalf("top_p = %v, want %v", opts.TopP, tc.topP)
			}
		})
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestDirectRequestTopP(t *testing.T) {
	for _, typ := range []string{"openai", "anthropic", "codex", "devin", "acp", "opencode"} {
		t.Run(typ, func(t *testing.T) {
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "p", Type: typ}}, Models: []config.ModelEntry{{Model: "p/test-model"}}}
			value := 0.7
			opts, err := directRequestOptions(cfg, "p/test-model", chatCompletionRequest{}, &value)
			if typ != "openai" {
				if err == nil {
					t.Fatalf("%s accepted top_p", typ)
				}
				return
			}
			if err != nil || opts.TopP == nil || *opts.TopP != value {
				t.Fatalf("opts=%+v err=%v", opts, err)
			}
		})
	}
}
