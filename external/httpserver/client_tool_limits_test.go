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

// getRaw reads path and returns the status and the answer.
func (s *openAIPassthroughState) getRaw(t *testing.T, path string) (int, string) {
	t.Helper()
	res, err := http.Get(s.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// TestModelListingAdvertisesTheLimits puts the bounds a request is measured
// against on every row of GET /v1/models (issue #577), so a client sizes a
// request instead of losing a turn to a 400 it could have avoided. A direct
// model row carries the configured tool bounds; a profile row runs coddy's own
// tools and carries none, which it says rather than leaving the reader to
// guess from an absent key; the picture bounds are on both.
func TestModelListingAdvertisesTheLimits(t *testing.T) {
	state := &openAIPassthroughState{}
	if err := state.reset(); err != nil {
		t.Fatal(err)
	}
	defer state.close()
	if err := state.startServer(); err != nil {
		t.Fatal(err)
	}

	read := func(t *testing.T, id string) gjson.Result {
		t.Helper()
		code, body := state.getRaw(t, "/v1/models")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/models: %d %s", code, body)
		}
		row := gjson.Get(body, `data.#(id=="`+id+`")`)
		if !row.Exists() {
			t.Fatalf("no row for %q: %s", id, body)
		}
		return row
	}

	t.Run("a direct model row, with the defaults", func(t *testing.T) {
		limits := read(t, "local/qwen3-1.7b").Get("limits")
		for key, want := range map[string]int64{
			"tools":              defaultMaxClientTools,
			"tool_schema_bytes":  defaultMaxClientToolSchemaBytes,
			"images_per_message": maxImagePartsPerMessage,
			"image_bytes":        maxImagePartBytes,
		} {
			if got := limits.Get(key).Int(); got != want {
				t.Errorf("limits.%s = %d, want %d: %s", key, got, want, limits.Raw)
			}
		}
		if !limits.Get("client_tools").Bool() {
			t.Errorf("limits.client_tools = false on a direct model row: %s", limits.Raw)
		}
	})

	t.Run("a direct model row without multimodal takes no pictures", func(t *testing.T) {
		// Its last message is refused with a picture (issue #584), so the row
		// must not advertise room for one.
		limits := read(t, "local/qwen3-text").Get("limits")
		if !limits.Get("images_per_message").Exists() {
			t.Fatal("limits.images_per_message is missing, want an explicit zero")
		}
		if got := limits.Get("images_per_message").Int(); got != 0 {
			t.Errorf("limits.images_per_message = %d, want 0: %s", got, limits.Raw)
		}
		if got := limits.Get("tools").Int(); got != defaultMaxClientTools {
			t.Errorf("limits.tools = %d, want %d: %s", got, defaultMaxClientTools, limits.Raw)
		}
	})

	t.Run("a raised limit is the one advertised", func(t *testing.T) {
		state.srv.activeCfg().HTTPServer.MaxClientTools = 200
		state.srv.activeCfg().HTTPServer.MaxClientToolSchemaBytes = 1 << 20
		limits := read(t, "local/qwen3-1.7b").Get("limits")
		if got := limits.Get("tools").Int(); got != 200 {
			t.Errorf("limits.tools = %d, want 200: %s", got, limits.Raw)
		}
		if got := limits.Get("tool_schema_bytes").Int(); got != 1<<20 {
			t.Errorf("limits.tool_schema_bytes = %d, want %d: %s", got, 1<<20, limits.Raw)
		}
	})

	t.Run("a profile row takes none of the client's tools", func(t *testing.T) {
		for _, id := range []string{"agent", "plan", "ask"} {
			limits := read(t, id).Get("limits")
			if limits.Get("client_tools").Bool() {
				t.Errorf("%s: limits.client_tools = true: %s", id, limits.Raw)
			}
			for _, key := range []string{"tools", "tool_schema_bytes"} {
				if limits.Get(key).Exists() {
					t.Errorf("%s: limits.%s is advertised for a row that refuses client tools: %s", id, key, limits.Raw)
				}
			}
			// A profile still takes the pictures of the last user message.
			if got := limits.Get("images_per_message").Int(); got != maxImagePartsPerMessage {
				t.Errorf("%s: limits.images_per_message = %d, want %d: %s", id, got, maxImagePartsPerMessage, limits.Raw)
			}
		}
	})
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

// responsesToolBody is a direct Responses request offering one tool of the
// given name and parameters, with an optional tool_choice.
func responsesToolBody(name, params, choice string) string {
	body := `{"model":"local/qwen3-1.7b","input":"hi","tools":[{"type":"function","name":"` + name + `","parameters":` + params + `}]`
	if choice != "" {
		body += `,"tool_choice":` + choice
	}
	return body + "}"
}

// TestClientToolErrorNamesTheFieldItConcerns keeps error.param on the field the
// refusal is about. Every message about a tool names that tool, and a client
// may call one "tool_choice", so reading the field back out of the message
// would answer such a request against a field it never sent; a refusal carries
// its own field instead. An unsupported tool_choice still gets its own.
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
			body:      responsesToolBody("tool_choice", big, ""),
			wantParam: "tools",
			wantCode:  "tool_parameters_too_large",
		},
		{
			name:      "a tool named tool_choice with invalid parameters",
			body:      responsesToolBody("tool_choice", `3`, ""),
			wantParam: "tools",
		},
		{
			name:      "a tool named tool_choice asking to be strict",
			body:      `{"model":"local/qwen3-1.7b","input":"hi","tools":[{"type":"function","name":"tool_choice","parameters":{"type":"object"},"strict":true}]}`,
			wantParam: "tools",
		},
		{
			name:      "an unsupported tool_choice",
			body:      responsesToolBody("t", `{"type":"object"}`, `"required"`),
			wantParam: "tool_choice",
		},
		{
			name:      "a tool_choice that is not a string",
			body:      responsesToolBody("t", `{"type":"object"}`, `{"type":"function"}`),
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

	// The chat endpoint rejects an unsupported tool_choice before the tool
	// list is read, so a refusal that reaches this writer there is always
	// about the tools, whatever a client named one of them.
	t.Run("chat completions, a tool named tool_choice with invalid parameters", func(t *testing.T) {
		state := &openAIPassthroughState{}
		if err := state.reset(); err != nil {
			t.Fatal(err)
		}
		defer state.close()
		if err := state.startServer(); err != nil {
			t.Fatal(err)
		}
		body := `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false,` +
			`"tools":[{"type":"function","function":{"name":"tool_choice","parameters":3}}]}`
		code, answer := state.postRaw(t, "/v1/chat/completions", body)
		if code != http.StatusBadRequest {
			t.Fatalf("%d %s", code, answer)
		}
		if got := gjson.Get(answer, "error.param").String(); got != "tools" {
			t.Errorf("error.param = %q, want tools: %s", got, answer)
		}
	})
}
