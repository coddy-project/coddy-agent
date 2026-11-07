package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func toolSchemaSizeFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/tool_schema_limit/kimi_size.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestToolSchemaLimitAcrossAdapters(t *testing.T) {
	// Only the Kimi body is a reported provider response. Replaying it through
	// other adapters checks error preservation, not those providers' wording.
	for name, client := range schemaFixtureClients() {
		t.Run(name, func(t *testing.T) {
			for path, err := range refusals(t, 400, toolSchemaSizeFixture(t), client) {
				if !IsToolSchemaLimit(err) {
					t.Fatalf("%s: not classified: %v", path, err)
				}
				if IsContextOverflow(err) || isRetryableLLMError(err) || IsTransientProviderError(err) {
					t.Fatalf("%s: schema refusal can trigger recovery", path)
				}
				if UpstreamStatus(err) != 400 {
					t.Fatalf("%s: status lost", path)
				}
			}
		})
	}
}

func schemaFixtureClients() map[string]func(string) Provider {
	return map[string]func(string) Provider{
		"openai compatible": openAIClient,
		"anthropic": func(url string) Provider {
			p := newAnthropicProvider("fixture", "fixture-key", url, nil, 100, 0, "", "")
			p.blocking = true
			return p
		},
		"kimi chat": func(url string) Provider {
			return newKimiChatProvider(schemaFixtureTransport(url), ProviderInput{Model: "fixture", MaxTokens: 100})
		},
		"kimi anthropic": func(url string) Provider {
			tr := schemaFixtureTransport(url)
			tr.anthropic = true
			return newKimiAnthropicProvider(tr, ProviderInput{Model: "fixture", MaxTokens: 100})
		},
		"kimi responses": func(url string) Provider {
			return newKimiResponsesProvider(schemaFixtureTransport(url), ProviderInput{Model: "fixture", MaxTokens: 100})
		},
	}
}

func TestToolSchemaLimitIndexMatchesCapturedOutgoingRequest(t *testing.T) {
	// These indexed diagnostics are synthetic compatibility cases. Compare
	// their attribution with the actual request after each adapter's encoding,
	// including Kimi Chat's reference expansion, rather than assuming order.
	for name, client := range schemaFixtureClients() {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", name, streaming), func(t *testing.T) {
				var captured struct {
					Tools []struct {
						Name     string
						Function *struct{ Name string }
					} `json:"tools"`
				}
				calls := 0
				path := "tools[1].function.parameters"
				if strings.Contains(name, "anthropic") {
					path = "tools.1.input_schema"
				}
				if strings.Contains(name, "responses") {
					path = "tools[1].parameters"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "invalid_request_error", "param": path, "message": "schema exceeds maximum allowed size"}})
				}))
				defer server.Close()
				schema := map[string]any{"type": "object", "$defs": map[string]any{"query": map[string]any{"type": "string"}}, "properties": map[string]any{"query": map[string]any{"$ref": "#/$defs/query"}}}
				tools := []ToolDefinition{{Name: "zebra", InputSchema: schema}, {Name: "docs__search", InputSchema: schema}}
				p := wrapResilient(client(server.URL), ResilientOptions{RetryMax: 2})
				messages := []Message{{Role: RoleUser, Content: "hello"}}
				var err error
				if streaming {
					_, err = p.Stream(context.Background(), messages, tools, func(StreamChunk) {})
				} else {
					_, err = p.Complete(context.Background(), messages, tools)
				}
				if err == nil || !IsToolSchemaLimit(err) || calls != 1 {
					t.Fatalf("classified=%v requests=%d error=%v", IsToolSchemaLimit(err), calls, err)
				}
				if len(captured.Tools) != 2 {
					t.Fatalf("offered %d tools, want 2", len(captured.Tools))
				}
				actualName := captured.Tools[1].Name
				if captured.Tools[1].Function != nil {
					actualName = captured.Tools[1].Function.Name
				}
				detail := ToolSchemaLimitDetailOf(err, tools)
				if actualName != "docs__search" || detail.Tool != actualName {
					t.Fatalf("actual outgoing tool=%q attributed=%q", actualName, detail.Tool)
				}
			})
		}
	}
}

func TestToolSchemaLimitAnthropicCompilationUsesOutgoingTools(t *testing.T) {
	// This exact diagnostic is documented for both strict tools and output
	// formats: https://platform.claude.com/docs/en/build-with-claude/structured-outputs
	const body = `{"type":"error","error":{"type":"invalid_request_error","message":"Schema is too complex for compilation."}}`
	for _, withTools := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("tools=%v/stream=%v", withTools, streaming), func(t *testing.T) {
				var captured map[string]json.RawMessage
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				p := schemaFixtureClients()["anthropic"](server.URL)
				var tools []ToolDefinition
				if withTools {
					tools = []ToolDefinition{{Name: "docs__search", InputSchema: map[string]any{"type": "object"}}}
				}
				var err error
				messages := []Message{{Role: RoleUser, Content: "hello"}}
				if streaming {
					_, err = p.Stream(context.Background(), messages, tools, nil)
				} else {
					_, err = p.Complete(context.Background(), messages, tools)
				}
				if err == nil || IsToolSchemaLimit(err) != withTools {
					t.Fatalf("classified=%v want=%v error=%v", IsToolSchemaLimit(err), withTools, err)
				}
				if _, hasTools := captured["tools"]; hasTools != withTools {
					t.Fatal("captured tools differ from offered tools")
				}
				if len(captured["output_config"]) != 0 {
					t.Fatal("unexpected outgoing output format")
				}
				if withTools {
					if detail := ToolSchemaLimitDetailOf(err, tools); detail.Tool != "docs__search" {
						t.Fatalf("single outgoing tool not identified: %#v", detail)
					}
					if isRetryableLLMError(err) || IsTransientProviderError(err) {
						t.Fatal("compilation refusal would be retried")
					}
				}
			})
		}
	}
	// A future adapter that also sends an output format must not use the bare
	// diagnostic to blame its tools. Preserve the cause in every case.
	cause := &httpStatusError{status: 400, body: body}
	for _, wire := range []string{
		`{"tools":[{"name":"search"}],"output_config":{"format":{"type":"json_schema"}}}`,
		`{"tools":[{"name":"search"}],"response_format":{"type":"json_schema"}}`,
		`{"tools":[{"name":"search"}],"text":{"format":{"type":"json_schema"}}}`,
	} {
		err := withToolSchemaRequest(cause, json.RawMessage(wire), true)
		if IsToolSchemaLimit(err) || !errors.Is(err, cause) {
			t.Fatal("ambiguous output format attributed to tools or cause lost")
		}
	}
}

func schemaFixtureTransport(url string) *KimiInferenceTransport {
	return &KimiInferenceTransport{endpoint: url, client: http.DefaultClient, source: func(context.Context) (string, error) { return "fixture-token", nil }}
}

func TestToolSchemaLimitClassification(t *testing.T) {
	// Synthetic compatibility cases isolate the required diagnostic evidence.
	cases := []struct {
		name, message string
		status        int
		want          bool
	}{
		{"moonshot depth", "tools.function.parameters: schema depth exceeds maximum limit of 8", 400, true},
		{"named function", "Invalid schema for function 'docs__search': schema is too complex for compilation.", 400, true},
		{"anthropic tools path", "tools.0.input_schema: Schema is too complex for compilation.", 400, true},
		{"google tools path", "tools[0].function_declarations[0].parameters: schema exceeds maximum allowed size", 400, true},
		{"index path", "tools[0].function.parameters: schema is too large", 422, true},
		{"no status", "tool schema exceeds maximum allowed size (500 properties)", 0, true},
		{"plain malformed schema", "Invalid schema for function 'search': additionalProperties is required", 400, false},
		{"schema property size keyword", "tools.function.parameters: property description has maxLength 500", 400, false},
		{"output only", "output_config.format: Schema is too complex for compilation.", 400, false},
		{"ambiguous compilation", "Schema is too complex for compilation.", 400, false},
		{"output with tools mentioned", "output_config.format: schema is too large; tools are enabled", 400, false},
		{"mixed output diagnostic", "output_config.format: schema is too complex; tools.0.input_schema is enabled", 400, false},
		{"output param", `{"error":{"param":"response_format","message":"tool schema is too large"}}`, 400, false},
		{"request bytes", "request body exceeds maximum allowed size", 413, false},
		{"context", "tools.function.parameters: maximum context length is 8192 tokens", 400, false},
		{"auth", "tools.function.parameters: schema exceeds maximum allowed size", 401, false},
		{"rate", "tools.function.parameters: schema exceeds maximum allowed size", 429, false},
		{"outage", "tools.function.parameters: schema exceeds maximum allowed size", 503, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := error(&httpStatusError{label: "fixture", status: tc.status, body: tc.message})
			if tc.status == 0 {
				err = errors.New(tc.message)
			}
			err = fmt.Errorf("provider: %w", err)
			if got := IsToolSchemaLimit(err); got != tc.want {
				t.Fatalf("classified=%v, want %v", got, tc.want)
			}
			if tc.want && (isRetryableLLMError(err) || IsTransientProviderError(err)) {
				t.Fatal("same schema request would be retried")
			}
		})
	}
	for _, err := range []error{nil, fmt.Errorf("tools.function.parameters: schema exceeds maximum allowed size: %w", context.Canceled), fmt.Errorf("tools.function.parameters: schema exceeds maximum allowed size: %w", context.DeadlineExceeded)} {
		if IsToolSchemaLimit(err) {
			t.Fatalf("cancel/deadline/nil classified: %v", err)
		}
	}
}

func TestToolSchemaLimitDetailNamesOnlyAnOfferedFunction(t *testing.T) {
	defs := []ToolDefinition{{Name: "docs__search"}, {Name: "read"}}
	for _, tc := range []struct{ message, want string }{
		{"Invalid schema for function 'docs__search': schema exceeds maximum allowed size", "docs__search"},
		{"Invalid schema for function 'not_offered': schema exceeds maximum allowed size", ""},
		{"tools[0].function.parameters: schema exceeds maximum allowed size", ""},
		{"tools.function.parameters: schema exceeds maximum allowed size", ""},
		{`{"error":{"message":"Invalid schema for function \"docs__search\": schema exceeds maximum allowed size"}}`, "docs__search"},
	} {
		d := ToolSchemaLimitDetailOf(errors.New(tc.message), defs)
		if d.Tool != tc.want {
			t.Fatalf("tool=%q want %q", d.Tool, tc.want)
		}
	}
	d := ToolSchemaLimitDetailOf(errors.New("tools.function.parameters: schema depth exceeds maximum limit of 8"), defs)
	if !strings.Contains(d.Limit, "8") {
		t.Fatalf("reported depth limit lost: %#v", d)
	}
}
