//go:build http

package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

func TestOpenAIUnsupportedParameters(t *testing.T) {
	cases := []struct{ name, field, param string }{
		{"unknown", `"future_option":true`, "future_option"},
		{"count", `"n":2`, "n"},
		{"structured output", `"response_format":{"type":"json_object"}`, "response_format"},
		{"required tool", `"tool_choice":"required"`, "tool_choice"},
		{"named tool", `"tool_choice":{"type":"function","function":{"name":"f"}}`, "tool_choice"},
		{"forced tool", `"tool_choice":{"type":"function","function":{"name":"f","extra":true}}`, "tool_choice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &aggregatorReasoningState{}
			if err := s.reset(); err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if err := s.anAggregatorServing("test-model"); err != nil {
				t.Fatal(err)
			}
			if err := s.aCoddyServerOnTheAggregator("test/test-model"); err != nil {
				t.Fatal(err)
			}
			body := `{"model":"test/test-model","messages":[{"role":"user","content":"hi"}],"stream":true,` + tc.field + `}`
			resp, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("close response body: %v", err)
				}
			}()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 400 || gjson.GetBytes(raw, "error.param").String() != tc.param {
				t.Fatalf("got status %d and body %s, want 400 param %s", resp.StatusCode, raw, tc.param)
			}
		})
	}
}

func TestOpenAINestedParametersRejected(t *testing.T) {
	cases := []struct{ name, fields, param string }{
		{"name", `"messages":[{"role":"user","content":"hi","name":"alice"}]`, "messages[0].name"},
		{"message", `"messages":[{"role":"user","content":"hi","other":1}]`, "messages[0].other"},
		{"call", `"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"},"extra":1}]}]`, "messages[0].tool_calls[0].extra"},
		{"function", `"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}","extra":1}}]}]`, "messages[0].tool_calls[0].function.extra"},
		{"detail", `"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"high"}}]}]`, "messages[0].content[0].image_url.detail"},
		{"reasoning", `"messages":[{"role":"user","content":"hi"}],"metadata":{"reasoning":"high"}`, "metadata.reasoning"},
		{"stream options", `"messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}`, "stream_options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &aggregatorReasoningState{}
			if err := s.reset(); err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if err := s.anAggregatorServing("test-model"); err != nil {
				t.Fatal(err)
			}
			if err := s.aCoddyServerOnTheAggregator("test/test-model"); err != nil {
				t.Fatal(err)
			}
			response, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"model":"test/test-model",`+tc.fields+`}`))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Errorf("close response body: %v", err)
				}
			}()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 400 || gjson.GetBytes(raw, "error.param").String() != tc.param {
				t.Fatalf("status=%d body=%s want param %s", response.StatusCode, raw, tc.param)
			}
			s.backend.mu.Lock()
			defer s.backend.mu.Unlock()
			if len(s.backend.chats) != 0 {
				t.Fatalf("unexpected upstream requests: %d", len(s.backend.chats))
			}
		})
	}
}
