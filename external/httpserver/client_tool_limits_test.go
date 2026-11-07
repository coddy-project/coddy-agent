//go:build http

package httpserver

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// numberedResponsesTools is n flat Responses function tools named t0, t1, ...
func numberedResponsesTools(n int) string {
	tools := make([]string, n)
	for i := range tools {
		tools[i] = fmt.Sprintf(`{"type":"function","name":"t%d","parameters":{"type":"object","properties":{}}}`, i)
	}
	return "[" + strings.Join(tools, ",") + "]"
}

// postRaw posts body to path and returns the status and the answer, whatever
// the status.
func (s *openAIPassthroughState) postRaw(t *testing.T, path, body string) (int, string) {
	t.Helper()
	res, err := http.Post(s.ts.URL+path, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// clientToolRequest is a direct request offering tools on one of the two
// endpoints that take client tools.
func clientToolRequest(endpoint string, n int) string {
	if endpoint == "/v1/responses" {
		return `{"model":"local/qwen3-1.7b","input":"hi","tools":` + numberedResponsesTools(n) + `}`
	}
	return `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false,"tools":` + numberedTools(n) + `}`
}

// TestClientToolLimit holds both endpoints to the configured number of client
// tools (issue #577): the default stays OpenAI's 128, a configured limit is
// honoured either way, and a refusal names the field, a code, the count, the
// limit and the setting, so the operator knows what to change.
func TestClientToolLimit(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			cases := []struct {
				name   string
				limit  int
				offer  int
				refuse bool
			}{
				{name: "the default takes 128", offer: 128},
				{name: "the default refuses 129", offer: 129, refuse: true},
				{name: "a raised limit takes 177", limit: 200, offer: 177},
				{name: "a lowered limit refuses 3", limit: 2, offer: 3, refuse: true},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					state := &openAIPassthroughState{}
					if err := state.reset(); err != nil {
						t.Fatal(err)
					}
					defer state.close()
					if err := state.startServer(); err != nil {
						t.Fatal(err)
					}
					state.srv.activeCfg().HTTPServer.MaxClientTools = tc.limit
					code, body := state.postRaw(t, endpoint, clientToolRequest(endpoint, tc.offer))
					if !tc.refuse {
						if code != http.StatusOK {
							t.Fatalf("%d tools under a limit of %d: %d %s", tc.offer, tc.limit, code, body)
						}
						return
					}
					limit := tc.limit
					if limit == 0 {
						limit = 128
					}
					if code != http.StatusBadRequest {
						t.Fatalf("%d tools over a limit of %d: %d %s", tc.offer, limit, code, body)
					}
					if got := gjson.Get(body, "error.param").String(); got != "tools" {
						t.Errorf("error.param = %q, want tools: %s", got, body)
					}
					if got := gjson.Get(body, "error.code").String(); got != "too_many_tools" {
						t.Errorf("error.code = %q, want too_many_tools: %s", got, body)
					}
					msg := gjson.Get(body, "error.message").String()
					for _, want := range []string{
						fmt.Sprintf("at most %d tools", limit),
						fmt.Sprintf("%d were offered", tc.offer),
						"httpserver.max_client_tools",
					} {
						if !strings.Contains(msg, want) {
							t.Errorf("error.message %q lacks %q", msg, want)
						}
					}
					if state.backend.last() != "" {
						t.Errorf("a refused tool list reached the provider")
					}
				})
			}
		})
	}
}

// TestClientToolSchemaLimit holds a tool's parameters to the configured size
// on both endpoints, with the same error shape.
func TestClientToolSchemaLimit(t *testing.T) {
	big := `{"type":"object","description":"` + strings.Repeat("x", 2000) + `"}`
	bodies := map[string]string{
		"/v1/chat/completions": `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false,"tools":[{"type":"function","function":{"name":"big","parameters":` + big + `}}]}`,
		"/v1/responses":        `{"model":"local/qwen3-1.7b","input":"hi","tools":[{"type":"function","name":"big","parameters":` + big + `}]}`,
	}
	for endpoint, body := range bodies {
		t.Run(endpoint, func(t *testing.T) {
			state := &openAIPassthroughState{}
			if err := state.reset(); err != nil {
				t.Fatal(err)
			}
			defer state.close()
			if err := state.startServer(); err != nil {
				t.Fatal(err)
			}
			if code, answer := state.postRaw(t, endpoint, body); code != http.StatusOK {
				t.Fatalf("a 2 KB schema under the default: %d %s", code, answer)
			}
			state.srv.activeCfg().HTTPServer.MaxClientToolSchemaBytes = 1024
			code, answer := state.postRaw(t, endpoint, body)
			if code != http.StatusBadRequest {
				t.Fatalf("a 2 KB schema over 1024 bytes: %d %s", code, answer)
			}
			if got := gjson.Get(answer, "error.param").String(); got != "tools" {
				t.Errorf("error.param = %q, want tools: %s", got, answer)
			}
			if got := gjson.Get(answer, "error.code").String(); got != "tool_parameters_too_large" {
				t.Errorf("error.code = %q, want tool_parameters_too_large: %s", got, answer)
			}
			msg := gjson.Get(answer, "error.message").String()
			for _, want := range []string{`tool "big"`, "exceed 1024 bytes", "httpserver.max_client_tool_schema_bytes"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error.message %q lacks %q", msg, want)
				}
			}
		})
	}
}

// TestClientToolErrorNamesTheFieldItConcerns keeps error.param on the field the
// refusal is about. The field of a refusal other than a broken bound is read
// out of its message, so a tool a client happens to name "tool_choice" has to
// stay a tools error, while an unsupported tool_choice stays its own.
func TestClientToolErrorNamesTheFieldItConcerns(t *testing.T) {
	big := `{"type":"object","description":"` + strings.Repeat("x", 2000) + `"}`
	cases := []struct {
		name      string
		body      string
		wantParam string
		wantCode  string
	}{
		{
			name:      "a tool named tool_choice over the schema bound",
			body:      `{"model":"local/qwen3-1.7b","input":"hi","tools":[{"type":"function","name":"tool_choice","parameters":` + big + `}]}`,
			wantParam: "tools",
			wantCode:  "tool_parameters_too_large",
		},
		{
			name:      "an unsupported tool_choice",
			body:      `{"model":"local/qwen3-1.7b","input":"hi","tools":[{"type":"function","name":"t","parameters":{"type":"object"}}],"tool_choice":"required"}`,
			wantParam: "tool_choice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &openAIPassthroughState{}
			if err := state.reset(); err != nil {
				t.Fatal(err)
			}
			defer state.close()
			if err := state.startServer(); err != nil {
				t.Fatal(err)
			}
			state.srv.activeCfg().HTTPServer.MaxClientToolSchemaBytes = 1024
			code, answer := state.postRaw(t, "/v1/responses", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("%d %s", code, answer)
			}
			if got := gjson.Get(answer, "error.param").String(); got != tc.wantParam {
				t.Errorf("error.param = %q, want %q: %s", got, tc.wantParam, answer)
			}
			if got := gjson.Get(answer, "error.code").String(); got != tc.wantCode {
				t.Errorf("error.code = %q, want %q: %s", got, tc.wantCode, answer)
			}
		})
	}
}
