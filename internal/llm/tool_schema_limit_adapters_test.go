package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolSchemaLimitGoogleHTTPAndStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			const path = "tools[0].functionDeclarations[0].parametersJsonSchema"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial answer\"}]}}]}\n\n")
					_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":%q}}\n\n", path+": schema exceeds maximum allowed size")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, strings.Repeat("x", 600)+" "+path+": schema exceeds maximum allowed size")
			}))
			defer server.Close()
			p := &opencodeGoogleProvider{input: ProviderInput{Model: "fixture"}, client: server.Client(), base: server.URL}
			tools := []ToolDefinition{{Name: "docs__search", InputSchema: map[string]any{"type": "object"}}}
			messages := []Message{{Role: RoleUser, Content: "hello"}}
			var response *Response
			var err error
			if streaming {
				response, err = p.Stream(context.Background(), messages, tools, func(StreamChunk) {})
			} else {
				response, err = p.Complete(context.Background(), messages, tools)
			}
			if !IsToolSchemaLimit(err) {
				t.Fatalf("not classified: %v", err)
			}
			if detail := ToolSchemaLimitDetailOf(err, tools); detail.Tool != "docs__search" {
				t.Fatalf("wrong attribution: %#v", detail)
			}
			if streaming && (response == nil || response.Content != "partial answer") {
				t.Fatalf("partial response lost: %#v", response)
			}
		})
	}
}

func TestToolSchemaLimitKimiInBandErrors(t *testing.T) {
	for _, name := range []string{"kimi chat", "kimi anthropic", "kimi responses"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				const message = "Invalid schema for function 'docs__search': schema depth exceeds maximum limit of 8"
				switch name {
				case "kimi chat":
					_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":%q}}\n\n", message)
				case "kimi anthropic":
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":%q}}\n\n", message)
				case "kimi responses":
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":%q}}}\n\n", message)
				}
			}))
			defer server.Close()
			p := schemaFixtureClients()[name](server.URL)
			tools := []ToolDefinition{{Name: "docs__search", InputSchema: map[string]any{"type": "object"}}}
			_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, tools, nil)
			if !IsToolSchemaLimit(err) {
				t.Fatalf("not classified: %v", err)
			}
			detail := ToolSchemaLimitDetailOf(err, tools)
			if detail.Tool != "docs__search" || detail.Limit != "maximum schema depth 8" {
				t.Fatalf("diagnostic provenance lost: %#v", detail)
			}
		})
	}
}

func TestToolSchemaLimitKimiAnthropicBareCompilationAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, message string
		classified               bool
		status                   int
	}{
		{name: "bare compilation", eventType: "invalid_request_error", message: "Schema is too complex for compilation.", classified: true},
		{name: "overload keeps raw diagnostic private", eventType: "overloaded_error", message: "schema is too complex for compilation. token=fixture-secret https://example.invalid", status: 529},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":{\"type\":%q,\"message\":%q}}\n\n", tc.eventType, tc.message)
			}))
			defer server.Close()
			p := schemaFixtureClients()["kimi anthropic"](server.URL)
			tools := []ToolDefinition{{Name: "docs__search", InputSchema: map[string]any{"type": "object"}}}
			_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, tools, nil)
			if IsToolSchemaLimit(err) != tc.classified || UpstreamStatus(err) != tc.status {
				t.Fatalf("classified=%v status=%d error=%v", IsToolSchemaLimit(err), UpstreamStatus(err), err)
			}
			if tc.classified {
				if detail := ToolSchemaLimitDetailOf(err, tools); detail.Tool != "docs__search" {
					t.Fatalf("bare diagnostic not attributed: %#v", detail)
				}
			} else {
				if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "example.invalid") || !strings.Contains(err.Error(), "kimi anthropic: server stream error") {
					t.Fatalf("raw overload diagnostic exposed: %v", err)
				}
			}
		})
	}
}

func TestToolSchemaLimitGoogleRejectsUnknownToolsGroup(t *testing.T) {
	cause := &httpStatusError{status: 400, body: "tools[1].functionDeclarations[0].parametersJsonSchema: schema exceeds maximum allowed size"}
	err := withToolSchemaRequest(cause, json.RawMessage(`{"tools":[{"functionDeclarations":[{"name":"docs__search"}]}]}`), false)
	if !IsToolSchemaLimit(err) {
		t.Fatal("valid schema refusal not classified")
	}
	if detail := ToolSchemaLimitDetailOf(err, []ToolDefinition{{Name: "docs__search"}}); detail.Tool != "" {
		t.Fatalf("unknown tools group attributed to %q", detail.Tool)
	}
}

func TestToolSchemaLimitGoogleSnakeCaseAttribution(t *testing.T) {
	cause := &httpStatusError{status: 400, body: "tools[0].function_declarations[0].parameters: schema exceeds maximum allowed size"}
	err := withToolSchemaRequest(cause, json.RawMessage(`{"tools":[{"functionDeclarations":[{"name":"docs__search"}]}]}`), false)
	if detail := ToolSchemaLimitDetailOf(err, []ToolDefinition{{Name: "docs__search"}}); detail.Tool != "docs__search" {
		t.Fatalf("snake-case declaration not attributed: %#v", detail)
	}
}

func TestToolSchemaLimitGoogleDisplayBodyStaysBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, "ordinary validation failure "+strings.Repeat("x", 1000))
	}))
	defer server.Close()
	p := &opencodeGoogleProvider{input: ProviderInput{Model: "fixture"}, client: server.Client(), base: server.URL}
	_, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}, nil)
	if IsToolSchemaLimit(err) {
		t.Fatal("ordinary validation failure misclassified")
	}
	if count := strings.Count(err.Error(), "x"); count > 512 {
		t.Fatalf("display body exceeds its previous bound: %d", count)
	}
}
